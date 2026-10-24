//go:build deployment

package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

var partialGenerationRE = regexp.MustCompile(`data-partial-shell-generation="">([a-f0-9]{32})</code>`)
var partialTitleRE = regexp.MustCompile(`<title>([^<]*)</title>`)
var partialMetaRE = regexp.MustCompile(`<meta\b[^>]*>`)
var partialJSONLDRE = regexp.MustCompile(`(?s)<script\b[^>]*\btype="application/ld\+json"[^>]*>(.*?)</script>`)
var partialChunkRE = regexp.MustCompile(`(?s)<template data-nextgo-chunk="request-content"([^>]*)>(.*?)</template>`)

type partialRepresentation struct {
	Shell, Region, Generation, Title, Description, MetaGeneration, JSONID, JSONName string
	Failed                                                                          bool
	Fallback                                                                        bool
}

func partialAttribute(tag, name string) string {
	match := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(tag)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}
func partialText(data, attribute string) string {
	match := regexp.MustCompile(`<[^>]*\b` + regexp.QuoteMeta(attribute) + `=""[^>]*>([^<]*)</`).FindStringSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}
func decodePartialRepresentation(data []byte, kind string) (partialRepresentation, error) {
	var result partialRepresentation
	var envelope nextgo.Envelope
	switch kind {
	case "document":
		match := partialChunkRE.FindSubmatchIndex(data)
		if len(match) != 6 {
			return result, fmt.Errorf("missing one inert partial region template")
		}
		if bytes.Count(data, []byte(`<template data-nextgo-chunk="request-content"`)) != 1 {
			return result, fmt.Errorf("duplicate partial region template")
		}
		result.Shell = string(data[:match[0]])
		result.Region = string(data[match[4]:match[5]])
		result.Failed = bytes.Contains(data[match[2]:match[3]], []byte(`data-nextgo-stream-error="true"`))
		result.Fallback = true
		title := partialTitleRE.FindStringSubmatch(result.Shell)
		if len(title) != 2 {
			return result, fmt.Errorf("missing partial document title")
		}
		result.Title = html.UnescapeString(title[1])
		for _, tag := range partialMetaRE.FindAllString(result.Shell, -1) {
			switch partialAttribute(tag, "name") {
			case "partial-shell-generation":
				result.MetaGeneration = partialAttribute(tag, "content")
			case "description":
				result.Description = partialAttribute(tag, "content")
			}
		}
		structured := partialJSONLDRE.FindStringSubmatch(result.Shell)
		if len(structured) != 2 {
			return result, fmt.Errorf("missing public structured metadata")
		}
		var value struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		}
		if err := json.Unmarshal([]byte(structured[1]), &value); err != nil {
			return result, err
		}
		result.JSONID, result.JSONName = value.Identifier, value.Name
	case "json":
		if err := json.Unmarshal(data, &envelope); err != nil {
			return result, err
		}
		offset := strings.Index(envelope.HTML, `data-partial-region=""`)
		if offset < 0 {
			return result, fmt.Errorf("buffered partial region is missing")
		}
		start := strings.LastIndex(envelope.HTML[:offset], "<section")
		if start < 0 {
			return result, fmt.Errorf("buffered region wrapper is missing")
		}
		result.Shell, result.Region = envelope.HTML[:start], envelope.HTML[start:]
	case "stream":
		var frames []navigation.StreamFrame
		decoder := &navigation.StreamDecoder{}
		if err := decoder.Feed(data, func(frame navigation.StreamFrame) error { frames = append(frames, frame); return nil }); err != nil {
			return result, err
		}
		if err := decoder.Finish(); err != nil {
			return result, err
		}
		if len(frames) != 3 || frames[0].Type != "shell" || frames[1].Type != "chunk" || frames[1].ID != "request-content" || frames[2].Type != "done" {
			return result, fmt.Errorf("partial stream must contain shell, one region chunk and done")
		}
		if err := json.Unmarshal(frames[0].Page, &envelope); err != nil {
			return result, err
		}
		result.Shell, result.Region, result.Failed, result.Fallback = envelope.HTML, frames[1].HTML, frames[1].Failed, true
	default:
		return result, fmt.Errorf("unknown partial representation")
	}
	if kind != "document" {
		result.Title, result.Description, result.MetaGeneration = envelope.Title, envelope.Description, envelope.Meta["partial-shell-generation"]
		found := false
		for _, element := range envelope.Head {
			if element.Tag == "script" && element.Attrs["type"] == "application/ld+json" {
				var value struct {
					Identifier string `json:"identifier"`
					Name       string `json:"name"`
				}
				if err := json.Unmarshal([]byte(element.Text), &value); err != nil {
					return result, err
				}
				result.JSONID, result.JSONName, found = value.Identifier, value.Name, true
			}
		}
		if !found {
			return result, fmt.Errorf("navigation lost public structured metadata")
		}
	}
	generation := partialGenerationRE.FindStringSubmatch(result.Shell)
	if len(generation) != 2 {
		return result, fmt.Errorf("public partial shell generation missing")
	}
	result.Generation = generation[1]
	return result, nil
}
func checkPartialPublic(t *testing.T, value partialRepresentation, slug string) {
	t.Helper()
	if value.Title != "Partial "+slug+" "+value.Generation || value.MetaGeneration != value.Generation || value.JSONID != value.Generation || value.JSONName != value.Title || !strings.Contains(value.Description, value.Generation) {
		t.Errorf("public body/title/meta/JSON-LD diverged: %+v", value)
	}
	for _, part := range []string{`data-partial-shell="` + slug + `"`, `data-partial-layout=""`, `aria-label="Partial shell counter"`, `data-count=""`, `disabled=""`} {
		if !strings.Contains(value.Shell, part) {
			t.Errorf("public shell missing%s", part)
		}
	}
	if value.Fallback && !strings.Contains(value.Shell, `data-partial-loading=""`) {
		t.Error("initial shell lost useful no-JS fallback")
	}
	if strings.Contains(value.Shell, `data-partial-request-id=""`) || strings.Contains(value.Shell, `aria-label="Partial request counter"`) {
		t.Error("current-request region was stored in the public shell")
	}
}
func checkPartialPrivate(t *testing.T, value partialRepresentation, slug, message, visitor, path string) (requestID, processID string) {
	t.Helper()
	for _, item := range []struct{ attribute, want string }{{"data-partial-message", message}, {"data-partial-visitor", visitor}, {"data-partial-region-slug", slug}, {"data-partial-region-prop", slug}, {"data-partial-region-path", path}} {
		if got := partialText(value.Region, item.attribute); got != item.want {
			t.Errorf("partial %s got%q want%q", item.attribute, got, item.want)
		}
	}
	requestID, processID = partialText(value.Region, "data-partial-request-id"), partialText(value.Region, "data-partial-region-process")
	for _, id := range []string{requestID, processID} {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Error("missing request/process identity", id)
		}
	}
	if !strings.Contains(value.Region, `aria-label="Partial request counter"`) {
		t.Error("current request region lost its server-rendered Go counter")
	}
	for _, private := range []string{message, visitor, requestID, processID} {
		if private != "" && (strings.Contains(value.Shell, private) || strings.Contains(value.Shell, html.EscapeString(private))) {
			t.Error("current request data leaked into the cached shell", private)
		}
	}
	return requestID, processID
}
func checkPartialPrivacyHeaders(t *testing.T, headers http.Header) {
	t.Helper()
	if !strings.Contains(headers.Get("Cache-Control"), "no-store") || headers.Get("X-Nextgo-Prefetch") != "" || headers.Get(navigation.StaleHeader) != "" || headers.Get("X-Nextgo-Partial") != "1" {
		t.Errorf("partial representation is publicly reusable or lacks provenance: %v", headers)
	}
}
func partialEnvironment(failRegion bool) []string {
	return []string{"NEXTGO_DEMO_PARTIAL_SHELL_FAIL=true", "NEXTGO_DEMO_PARTIAL_REGION_FAIL=" + strconv.FormatBool(failRegion)}
}
func partialRequest(t *testing.T, client *http.Client, origin, path, message, visitor, kind string) (partialRepresentation, http.Header) {
	t.Helper()
	query := url.Values{"message": {message}, "delay": {"0s"}}
	request, err := http.NewRequest("GET", origin+path+"?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "nextgo_partial_name", Value: visitor})
	if kind != "document" {
		request.Header.Set("X-Nextgo-Navigate", "1")
	}
	if kind == "stream" {
		request.Header.Set(navigation.StreamHeader, strconv.Itoa(navigation.StreamVersion))
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data := body(t, response)
	if response.StatusCode != 200 {
		t.Fatalf("partial request%d: %s", response.StatusCode, data)
	}
	value, err := decodePartialRepresentation(data, kind)
	if err != nil {
		t.Fatal(err, string(data))
	}
	return value, response.Header.Clone()
}
func waitPartialReady(t *testing.T, client *http.Client, p *process, origin string) {
	t.Helper()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(origin + "/api/health")
		if err == nil {
			data := body(t, response)
			if response.StatusCode == 200 && bytes.Contains(data, []byte(`"ok":true`)) {
				return
			}
		}
		select {
		case <-p.done:
			t.Fatalf("partial app exited: %v\n%s", p.err, p.logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("partial app readiness timeout", p.logs.String())
}
func TestPartialHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, partialEnvironment(false), "-addr", addr)
	origin := "http://" + addr
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	waitPartialReady(t, client, p, origin)
	var rootGeneration, rootProcess, firstRequest string
	for i, test := range []struct{ kind, message, visitor string }{{"document", "private first <message> & one", "private-alice"}, {"json", "private second <message> & two", "private-bob"}, {"stream", "private third <message> & three", "private-carol"}} {
		value, headers := partialRequest(t, client, origin, "/partial", test.message, test.visitor, test.kind)
		checkPartialPublic(t, value, "root")
		checkPartialPrivacyHeaders(t, headers)
		requestID, processID := checkPartialPrivate(t, value, "root", test.message, test.visitor, "/partial")
		if value.Failed || headers.Get("X-Nextgo-Prerender") != "build" {
			t.Error("known shell was regenerated or lost its current region", headers, value.Failed)
		}
		if i == 0 {
			rootGeneration, rootProcess, firstRequest = value.Generation, processID, requestID
		} else if value.Generation != rootGeneration || processID != rootProcess || requestID == firstRequest {
			t.Error("public shell changed or request data was reused")
		}
	}
	alpha, headers := partialRequest(t, client, origin, "/partial/alpha", "private alpha", "private-dan", "document")
	checkPartialPublic(t, alpha, "alpha")
	checkPartialPrivacyHeaders(t, headers)
	_, _ = checkPartialPrivate(t, alpha, "alpha", "private alpha", "private-dan", "/partial/alpha")
	if alpha.Generation == rootGeneration {
		t.Error("different partial routes share shell identity")
	}
	beta, headers := partialRequest(t, client, origin, "/partial/beta", "private beta", "private-erin", "stream")
	checkPartialPublic(t, beta, "beta")
	checkPartialPrivacyHeaders(t, headers)
	_, _ = checkPartialPrivate(t, beta, "beta", "private beta", "private-erin", "/partial/beta")
	request, err := http.NewRequest("GET", origin+"/partial?message=must-not-prefetch", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Nextgo-Navigate", "1")
	request.Header.Set(navigation.PurposeHeader, "prefetch")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	prefetch := body(t, response)
	if response.StatusCode != 204 || len(prefetch) != 0 || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Error("partial prefetch retained personalized output", response.StatusCode, response.Header, string(prefetch))
	}
	p.stop(t)
	restarted := start(t, partialEnvironment(false), "-addr", addr)
	waitPartialReady(t, client, restarted, origin)
	value, _ := partialRequest(t, client, origin, "/partial", "private after restart", "private-frank", "document")
	checkPartialPublic(t, value, "root")
	_, processID := checkPartialPrivate(t, value, "root", "private after restart", "private-frank", "/partial")
	if value.Generation != rootGeneration || processID == rootProcess {
		t.Error("app restart lost built shell or reused another process region identity")
	}
	restarted.stop(t)
	// An unknown closed-list path follows the route's normal not-found/layout
	// rendering. The no-static-work guarantee applies to known shell hits, so
	// verify that separate path with an available static layout provider.
	healthy := start(t, []string{"NEXTGO_DEMO_PARTIAL_SHELL_FAIL=false", "NEXTGO_DEMO_PARTIAL_REGION_FAIL=false"}, "-addr", addr)
	waitPartialReady(t, client, healthy, origin)
	response, err = client.Get(origin + "/partial/not-built")
	if err != nil {
		t.Fatal(err)
	}
	unknown := body(t, response)
	if response.StatusCode != 404 || bytes.Contains(unknown, []byte(`data-partial-region=""`)) {
		t.Error("partial closed-list fallback did not remain a custom404", response.StatusCode)
	}
	healthy.stop(t)
}
func TestPartialRegionFailureHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, partialEnvironment(true), "-addr", addr)
	defer p.stop(t)
	origin := "http://" + addr
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	waitPartialReady(t, client, p, origin)
	for _, kind := range []string{"document", "stream"} {
		value, headers := partialRequest(t, client, origin, "/partial/alpha", "private-region-failure-sentinel", "private-failure-cookie", kind)
		checkPartialPublic(t, value, "alpha")
		checkPartialPrivacyHeaders(t, headers)
		if !value.Failed || !strings.Contains(value.Region, "Unable to load content.") || strings.Contains(value.Region, "private partial") || strings.Contains(value.Region, "private-region-failure-sentinel") || strings.Contains(value.Region, "private-failure-cookie") {
			t.Error("late region failure exposed detail, private data, or lost generic fallback", value.Region)
		}
	}
}

type partialLambdaEvent struct{ path, slug, kind, message, visitor string }

func TestPartialLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, failRegion := range []bool{false, true} {
			name := "current-request-isolation"
			if failRegion {
				name = "late-error"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				events := []partialLambdaEvent{{"/partial", "root", "document", "private lambda first <value>", "private-lambda-alice"}, {"/partial", "root", "stream", "private lambda second <value>", "private-lambda-bob"}, {"/partial/alpha", "alpha", "stream", "private lambda alpha", "private-lambda-carol"}}
				if failRegion {
					events = events[:2]
				}
				var issued, reports atomic.Int32
				done := make(chan struct{})
				var firstGeneration, firstRequest, processID string
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
						headers := map[string]string{"host": "partial.test"}
						if event.kind != "document" {
							headers["x-nextgo-navigate"] = "1"
						}
						if event.kind == "stream" {
							headers["x-nextgo-stream"] = "1"
						}
						w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
						w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
						_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": event.path, "rawQueryString": url.Values{"message": {event.message}, "delay": {"0s"}}.Encode(), "headers": headers, "cookies": []string{"nextgo_partial_name=" + event.visitor}, "requestContext": map[string]any{"domainName": "partial.test", "http": map[string]string{"method": "GET", "path": event.path}}})
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
							t.Error("missing partial Lambda stream metadata")
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
						t.Error("extra partial Lambda response")
						w.WriteHeader(500)
						return
					}
					event := events[index]
					if envelope.StatusCode != 200 || sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Prerender") != "build" {
						t.Error("Lambda partial shell was not served from production artifact", envelope.StatusCode, envelope.Headers)
					}
					headers := make(http.Header)
					for key, value := range envelope.Headers {
						headers.Set(key, value)
					}
					checkPartialPrivacyHeaders(t, headers)
					value, err := decodePartialRepresentation(payload, event.kind)
					if err != nil {
						t.Error(err, string(payload))
						w.WriteHeader(500)
						return
					}
					checkPartialPublic(t, value, event.slug)
					if index == 0 {
						firstGeneration = value.Generation
					} else if event.slug == "root" && value.Generation != firstGeneration {
						t.Error("Lambda invocation changed the reusable public shell")
					}
					if failRegion {
						if !value.Failed || !strings.Contains(value.Region, "Unable to load content.") || strings.Contains(value.Region, "private partial") || strings.Contains(value.Region, event.message) || strings.Contains(value.Region, event.visitor) {
							t.Error("Lambda late region error exposed private data or lost generic boundary", value.Region)
						}
					} else {
						id, nonce := checkPartialPrivate(t, value, event.slug, event.message, event.visitor, event.path)
						if index == 0 {
							firstRequest, processID = id, nonce
						} else if id == firstRequest || nonce != processID {
							t.Error("Lambda reused private request data or changed its worker identity")
						}
					}
					w.WriteHeader(202)
				}))
				defer runtime.Close()
				env := append(partialEnvironment(failRegion), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
				p := start(t, env)
				defer closeSeedChild(p)
				select {
				case <-done:
				case <-p.done:
					t.Fatalf("partial Lambda worker stopped: %v\n%s", p.err, p.logs.String())
				case <-time.After(15 * time.Second):
					t.Fatal("partial Lambda timeout", p.logs.String())
				}
				p.stop(t)
				if reports.Load() != int32(len(events)) {
					t.Error("missing partial Lambda invocations", reports.Load(), len(events))
				}
			})
		}
	}
}
