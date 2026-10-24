//go:build deployment

package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type partialShellHTTPReply struct {
	status  int
	headers http.Header
	data    []byte
}

func partialShellHTTPRequest(t *testing.T, client *http.Client, origin, path, message, visitor, purpose, capability, hint, kind string) partialShellHTTPReply {
	t.Helper()
	query := url.Values{"message": {message}, "delay": {"0s"}}
	request, err := http.NewRequest("GET", origin+path+"?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Nextgo-Navigate", "1")
	if purpose != "" {
		request.Header.Set(navigation.PurposeHeader, purpose)
	}
	if capability != "" {
		request.Header.Set(navigation.ShellProtocolHeader, capability)
	}
	if hint != "" {
		request.Header.Set(navigation.ShellHeader, hint)
	}
	if kind == "stream" {
		request.Header.Set(navigation.StreamHeader, "1")
	}
	request.AddCookie(&http.Cookie{Name: "nextgo_partial_name", Value: visitor})
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return partialShellHTTPReply{response.StatusCode, response.Header.Clone(), body(t, response)}
}
func partialShellPrefetched(t *testing.T, reply partialShellHTTPReply, slug string) nextgo.Envelope {
	t.Helper()
	if reply.status != 200 || reply.headers.Get("Cache-Control") != "private, no-store" || reply.headers.Get("X-Nextgo-Prefetch") != "shell" || reply.headers.Get(navigation.ShellProtocolHeader) != "1" || reply.headers.Get(navigation.StaleHeader) != "" {
		t.Fatal("public fallback shell was not safely negotiated", reply.status, reply.headers, string(reply.data))
	}
	var cached nextgo.Envelope
	if err := json.Unmarshal(reply.data, &cached); err != nil {
		t.Fatal(err)
	}
	ttl, err := strconv.Atoi(reply.headers.Get(navigation.ShellStaleHeader))
	if err != nil || ttl <= 0 || ttl > navigation.MaxShellStaleMillis {
		t.Fatal("invalid public-shell retention budget", ttl, err)
	}
	if !navigation.ValidShellID(cached.Shell) || cached.ShellRef || !strings.Contains(cached.HTML, `data-partial-loading=""`) || strings.Contains(cached.HTML, `data-partial-region=""`) || strings.Contains(cached.HTML, `data-partial-request-id=""`) || strings.Contains(cached.HTML, `aria-label="Partial request counter"`) {
		t.Fatal("prefetch retained a private region or omitted fallback ownership", cached)
	}
	for _, private := range []string{"private-prefetch-message", "private-prefetch-cookie"} {
		if strings.Contains(cached.HTML, private) || strings.Contains(cached.Title, private) || strings.Contains(cached.Description, private) {
			t.Fatal("private prefetch request entered the public shell", private)
		}
	}
	generation := partialGenerationRE.FindStringSubmatch(cached.HTML)
	if len(generation) != 2 || cached.Meta["partial-shell-generation"] != generation[1] || cached.Title != "Partial "+slug+" "+generation[1] {
		t.Fatal("prefetched public shell/head mismatch", cached)
	}
	return cached
}
func partialShellResumed(t *testing.T, reply partialShellHTTPReply, cached nextgo.Envelope, wantRef bool) (partialRepresentation, nextgo.Envelope) {
	t.Helper()
	if reply.status != 200 || reply.headers.Get("Cache-Control") != "private, no-store" || reply.headers.Get("X-Nextgo-Prefetch") != "" || reply.headers.Get(navigation.StaleHeader) != "" {
		t.Fatal("active shell response acquired whole-page reuse", reply.status, reply.headers, string(reply.data))
	}
	var frames []navigation.StreamFrame
	decoder := &navigation.StreamDecoder{}
	if err := decoder.Feed(reply.data, func(frame navigation.StreamFrame) error { frames = append(frames, frame); return nil }); err != nil {
		t.Fatal(err, string(reply.data))
	}
	if err := decoder.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[0].Type != "shell" || frames[1].Type != "chunk" || frames[1].ID != "request-content" || frames[2].Type != "done" {
		t.Fatal("invalid resumed frame ownership", frames)
	}
	var envelope nextgo.Envelope
	if err := json.Unmarshal(frames[0].Page, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ShellRef != wantRef || wantRef && (envelope.Shell != cached.Shell || envelope.HTML != "") || !wantRef && envelope.HTML == "" {
		t.Fatal("server reused an unowned shell or resent a matched fallback", envelope)
	}
	if wantRef {
		if envelope.Title != cached.Title || envelope.Description != cached.Description || envelope.Meta["partial-shell-generation"] != cached.Meta["partial-shell-generation"] || envelope.Build != cached.Build || envelope.Configuration != cached.Configuration {
			t.Fatal("reference metadata belongs to another shell", envelope, cached)
		}
		// Model the browser join using only the earlier public fallback. Private
		// chunks are decoded separately and are never written into cached.HTML.
		join := envelope
		join.HTML = cached.HTML
		data, err := json.Marshal(join)
		if err != nil {
			t.Fatal(err)
		}
		frames[0].Page = data
	}
	var joined bytes.Buffer
	for _, frame := range frames {
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		joined.Write(data)
		joined.WriteByte('\n')
	}
	value, err := decodePartialRepresentation(joined.Bytes(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	return value, envelope
}
func TestPartialShellPrefetchHTTPExecutable(t *testing.T) {
	guardAddr, activeAddr := address(t), address(t)
	guard := start(t, partialEnvironment(true), "-addr", guardAddr)
	defer guard.stop(t)
	active := start(t, partialEnvironment(false), "-addr", activeAddr)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	guardOrigin, activeOrigin := "http://"+guardAddr, "http://"+activeAddr
	waitPartialReady(t, client, guard, guardOrigin)
	waitPartialReady(t, client, active, activeOrigin)
	skipped := partialShellHTTPRequest(t, client, guardOrigin, "/partial/alpha", "private-prefetch-message", "private-prefetch-cookie", "prefetch", "", "", "json")
	if skipped.status != 204 || len(skipped.data) != 0 || skipped.headers.Get("X-Nextgo-Prefetch") != "" {
		t.Fatal("old prefetch behavior changed", skipped.status, skipped.headers)
	}
	cached := partialShellPrefetched(t, partialShellHTTPRequest(t, client, guardOrigin, "/partial/alpha", "private-prefetch-message", "private-prefetch-cookie", "prefetch", "1", "", "json"), "alpha")
	// The prefetch worker's registered private provider always fails. Its public
	// reply succeeds, while a different worker must run every active region.
	var firstID, firstProcess string
	for i, test := range []struct{ hint, message, visitor string }{{cached.Shell, "private-active-one", "private-active-alice"}, {cached.Shell, "private-active-two", "private-active-bob"}, {strings.Repeat("f", 64), "private-hint-miss", "private-active-carol"}, {"bad-hint", "private-malformed-hint", "private-active-dan"}} {
		reply := partialShellHTTPRequest(t, client, activeOrigin, "/partial/alpha", test.message, test.visitor, "", "1", test.hint, "stream")
		value, envelope := partialShellResumed(t, reply, cached, i < 2)
		checkPartialPublic(t, value, "alpha")
		id, process := checkPartialPrivate(t, value, "alpha", test.message, test.visitor, "/partial/alpha")
		if !navigation.ValidShellID(envelope.Shell) || envelope.Shell != cached.Shell || value.Failed {
			t.Fatal("same-build alpha shell changed or failed", envelope)
		}
		if i == 0 {
			firstID, firstProcess = id, process
		} else if id == firstID || process != firstProcess {
			t.Error("active resume reused a private request or another worker identity")
		}
	}
	betaReply := partialShellHTTPRequest(t, client, activeOrigin, "/partial/beta", "private-beta-current", "private-beta-visitor", "", "1", cached.Shell, "stream")
	beta, betaEnvelope := partialShellResumed(t, betaReply, cached, false)
	checkPartialPublic(t, beta, "beta")
	_, _ = checkPartialPrivate(t, beta, "beta", "private-beta-current", "private-beta-visitor", "/partial/beta")
	if !navigation.ValidShellID(betaEnvelope.Shell) || betaEnvelope.Shell == cached.Shell {
		t.Fatal("path ownership did not replace the proposed alpha shell")
	}
	legacy := partialShellHTTPRequest(t, client, activeOrigin, "/partial/alpha", "private-legacy-current", "private-legacy-visitor", "", "1", cached.Shell, "json")
	var legacyEnvelope nextgo.Envelope
	if err := json.Unmarshal(legacy.data, &legacyEnvelope); err != nil {
		t.Fatal(err)
	}
	if legacyEnvelope.Shell != "" || legacyEnvelope.ShellRef || !strings.Contains(legacyEnvelope.HTML, "private-legacy-current") {
		t.Fatal("legacy JSON retained a public-only reference", legacyEnvelope)
	}
	failureReply := partialShellHTTPRequest(t, client, guardOrigin, "/partial/alpha", "private-failure-current", "private-failure-visitor", "", "1", cached.Shell, "stream")
	failed, _ := partialShellResumed(t, failureReply, cached, true)
	checkPartialPublic(t, failed, "alpha")
	if !failed.Failed || !strings.Contains(failed.Region, "Unable to load content.") || strings.Contains(failed.Region, "private partial") || strings.Contains(failed.Region, "private-failure") {
		t.Fatal("resumed late failure exposed private data", failed.Region)
	}
	active.stop(t)
	active = start(t, partialEnvironment(false), "-addr", activeAddr)
	waitPartialReady(t, client, active, activeOrigin)
	resumed, _ := partialShellResumed(t, partialShellHTTPRequest(t, client, activeOrigin, "/partial/alpha", "private-after-restart", "private-restarted-visitor", "", "1", cached.Shell, "stream"), cached, true)
	_, process := checkPartialPrivate(t, resumed, "alpha", "private-after-restart", "private-restarted-visitor", "/partial/alpha")
	if process == firstProcess {
		t.Error("new worker reused old private process data")
	}
	active.stop(t)
	if strings.Contains(cached.HTML, "private-active") || strings.Contains(cached.HTML, "private-after-restart") {
		t.Fatal("audit client accidentally retained a private chunk in its public cache")
	}
}

type partialShellLambdaEvent struct{ path, message, visitor, purpose, capability, hint, kind string }

func runPartialShellLambda(t *testing.T, mode string, fail bool, events []partialShellLambdaEvent) []partialShellHTTPReply {
	t.Helper()
	var issued, reports atomic.Int32
	done := make(chan struct{})
	results := make(chan partialShellHTTPReply, len(events))
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			index := int(issued.Add(1)) - 1
			if index >= len(events) {
				if index == len(events) {
					close(done)
				}
				<-r.Context().Done()
				return
			}
			event := events[index]
			headers := map[string]string{"host": "partial-shell.test", "x-nextgo-navigate": "1"}
			if event.purpose != "" {
				headers["x-nextgo-purpose"] = event.purpose
			}
			if event.capability != "" {
				headers["x-nextgo-shell-protocol"] = event.capability
			}
			if event.hint != "" {
				headers["x-nextgo-shell"] = event.hint
			}
			if event.kind == "stream" {
				headers["x-nextgo-stream"] = "1"
			}
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": event.path, "rawQueryString": url.Values{"message": {event.message}, "delay": {"0s"}}.Encode(), "headers": headers, "cookies": []string{"nextgo_partial_name=" + event.visitor}, "requestContext": map[string]any{"domainName": "partial-shell.test", "http": map[string]string{"method": "GET", "path": event.path}}})
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
		}
		var envelope httpEnvelope
		var payload []byte
		if mode == "buffered" {
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Error(err)
			}
			payload = []byte(envelope.Body)
			if envelope.Base64 {
				payload, err = base64.StdEncoding.DecodeString(envelope.Body)
				if err != nil {
					t.Error(err)
				}
			}
		} else {
			split := bytes.Index(data, make([]byte, 8))
			if split < 0 {
				t.Error("missing public-shell Lambda metadata delimiter")
				w.WriteHeader(500)
				return
			}
			if err := json.Unmarshal(data[:split], &envelope); err != nil {
				t.Error(err)
			}
			payload = data[split+8:]
		}
		index := int(reports.Add(1)) - 1
		if index >= len(events) {
			t.Error("extra public-shell Lambda report")
			w.WriteHeader(500)
			return
		}
		headers := make(http.Header)
		for key, value := range envelope.Headers {
			headers.Set(key, value)
		}
		results <- partialShellHTTPReply{status: envelope.StatusCode, headers: headers, data: payload}
		w.WriteHeader(202)
	}))
	defer runtime.Close()
	env := append(partialEnvironment(fail), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
	p := start(t, env)
	defer closeSeedChild(p)
	select {
	case <-done:
	case <-p.done:
		t.Fatalf("public shell worker stopped: %v\n%s", p.err, p.logs.String())
	case <-time.After(15 * time.Second):
		t.Fatal("public-shell Lambda flow timeout", p.logs.String())
	}
	p.stop(t)
	if reports.Load() != int32(len(events)) {
		t.Fatal("missing public-shell Lambda reports", reports.Load(), len(events))
	}
	values := make([]partialShellHTTPReply, len(events))
	for i := range values {
		values[i] = <-results
	}
	return values
}
func TestPartialShellPrefetchLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			guarded := runPartialShellLambda(t, mode, true, []partialShellLambdaEvent{{path: "/partial/alpha", message: "private-prefetch-message", visitor: "private-prefetch-cookie", purpose: "prefetch", capability: "1", kind: "json"}})
			cached := partialShellPrefetched(t, guarded[0], "alpha")
			events := []partialShellLambdaEvent{{path: "/partial/alpha", message: "private-lambda-first", visitor: "private-lambda-alice", capability: "1", hint: cached.Shell, kind: "stream"}, {path: "/partial/alpha", message: "private-lambda-second", visitor: "private-lambda-bob", capability: "1", hint: cached.Shell, kind: "stream"}, {path: "/partial/beta", message: "private-lambda-beta", visitor: "private-lambda-carol", capability: "1", hint: cached.Shell, kind: "stream"}, {path: "/partial/alpha", message: "private-lambda-legacy", visitor: "private-lambda-dan", hint: cached.Shell, kind: "stream"}}
			replies := runPartialShellLambda(t, mode, false, events)
			var requestID, processID string
			for i, reply := range replies {
				event := events[i]
				value, envelope := partialShellResumed(t, reply, cached, i < 2)
				slug := "alpha"
				if i == 2 {
					slug = "beta"
				}
				checkPartialPublic(t, value, slug)
				id, process := checkPartialPrivate(t, value, slug, event.message, event.visitor, event.path)
				if i == 0 {
					requestID, processID = id, process
				} else if id == requestID || process != processID {
					t.Error("Lambda reference reused private request data or another worker identity")
				}
				if i < 2 && envelope.Shell != cached.Shell || i == 2 && (!navigation.ValidShellID(envelope.Shell) || envelope.Shell == cached.Shell) || i == 3 && (envelope.Shell != "" || envelope.ShellRef) {
					t.Fatal("Lambda mixed-version/path shell ownership failed", i, envelope)
				}
			}
			failed := runPartialShellLambda(t, mode, true, events[:1])
			value, _ := partialShellResumed(t, failed[0], cached, true)
			if !value.Failed || !strings.Contains(value.Region, "Unable to load content.") || strings.Contains(value.Region, "private partial") || strings.Contains(value.Region, events[0].message) {
				t.Error("resumed Lambda late failure lost its generic boundary", value.Region)
			}
		})
	}
}
