//go:build deployment

package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const generatedActionPath = "/_nextgo/actions/generated/add/v1"

type generatedActionScope struct {
	base, locale string
	slash        bool
}

func (s generatedActionScope) prefix() string {
	if s.locale != "" {
		return s.base + "/" + s.locale
	}
	return s.base
}
func (s generatedActionScope) page() string {
	path := s.prefix() + "/generated-actions"
	if s.slash {
		path += "/"
	}
	return path
}
func (s generatedActionScope) endpoint() string { return s.prefix() + generatedActionPath }
func (s generatedActionScope) environment() []string {
	values := []string{"NEXTGO_DEMO_BASE_PATH=" + s.base, "NEXTGO_DEMO_TRAILING_SLASH=" + strconv.FormatBool(s.slash), "NEXTGO_DEMO_READER_TOKEN=" + readerToken, "NEXTGO_DEMO_ADMIN_TOKEN=" + adminToken}
	if s.locale != "" {
		values = append(values, "NEXTGO_DEMO_LOCALES=true", "NEXTGO_DEMO_LOCALE_DETECT=false")
	}
	return values
}

type generatedSnapshot struct{ count, revision, load int64 }

func generatedActionSnapshot(data []byte, scope generatedActionScope) (generatedSnapshot, error) {
	var result generatedSnapshot
	var componentRevision int64
	for _, item := range []struct {
		attribute string
		value     *int64
	}{{"data-generated-count", &result.count}, {"data-generated-ssr-revision", &result.revision}, {"data-generated-revision", &componentRevision}, {"data-generated-ssr-load", &result.load}} {
		match := regexp.MustCompile(`<output[^>]*\b` + item.attribute + `=""[^>]*>([0-9]+)</output>`).FindSubmatch(data)
		if len(match) != 2 {
			return result, fmt.Errorf("missing generated SSR field %s", item.attribute)
		}
		value, err := strconv.ParseInt(string(match[1]), 10, 64)
		if err != nil {
			return result, err
		}
		*item.value = value
	}
	expected := fmt.Sprintf("Generated actions revision %d — Next.go", result.revision)
	metaMatches := false
	for _, tag := range regexp.MustCompile(`<meta\b[^>]*>`).FindAll(data, -1) {
		if bytes.Contains(tag, []byte(`name="generated-action-revision"`)) && bytes.Contains(tag, []byte(fmt.Sprintf(`content="%d"`, result.revision))) {
			metaMatches = true
		}
	}
	if componentRevision != result.revision || !metaMatches || !bytes.Contains(data, []byte("<title>"+expected+"</title>")) || !bytes.Contains(data, []byte(`action="`+scope.endpoint()+`"`)) || !bytes.Contains(data, []byte(`method="post"`)) || !bytes.Contains(data, []byte("Save with native generated form")) || !bytes.Contains(data, []byte("public process-local counter resets on restart")) {
		return result, fmt.Errorf("generated page lost metadata, native form path or process-local disclosure")
	}
	return result, nil
}

type generatedActionReply struct {
	status  int
	headers http.Header
	data    []byte
}
type generatedActionBody struct {
	Data *struct {
		Count    int64 `json:"count"`
		Revision int64 `json:"revision"`
	} `json:"data"`
	Redirect string `json:"redirect"`
	Error    *struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

func generatedActionMetadata(reply generatedActionReply, typed bool) error {
	if reply.headers.Get("X-Nextgo-Action-Protocol") != "1" || !strings.Contains(reply.headers.Get("Cache-Control"), "no-store") {
		return fmt.Errorf("generated action lost protocol/no-store metadata: %v", reply.headers)
	}
	if typed && reply.headers.Get("X-Nextgo-Action-Revision") != "v1" {
		return fmt.Errorf("generated contract revision not registered: %v", reply.headers)
	}
	if bytes.Contains(reply.data, []byte("private generated demo storage detail")) {
		return fmt.Errorf("private handler failure leaked into a public response")
	}
	return nil
}
func generatedActionResult(reply generatedActionReply, count, revision int64, destination string) error {
	if err := generatedActionMetadata(reply, true); err != nil {
		return err
	}
	var body generatedActionBody
	if err := json.Unmarshal(reply.data, &body); err != nil {
		return err
	}
	if reply.status != 200 || body.Error != nil || body.Data == nil || body.Data.Count != count || body.Data.Revision != revision || body.Redirect != destination {
		return fmt.Errorf("generated confirmation status/data/redirect mismatch: %d %s", reply.status, reply.data)
	}
	return nil
}
func generatedActionFailure(reply generatedActionReply, status int, code string, typed bool) error {
	if err := generatedActionMetadata(reply, typed); err != nil {
		return err
	}
	var body generatedActionBody
	if err := json.Unmarshal(reply.data, &body); err != nil {
		return err
	}
	if reply.status != status || body.Data != nil || body.Error == nil || body.Error.Code != code {
		return fmt.Errorf("generated rejection got%d %s, want%d/%s", reply.status, reply.data, status, code)
	}
	return nil
}
func generatedActionHTTP(t *testing.T, client *http.Client, origin, path, payload, media, accept string, headers http.Header) generatedActionReply {
	t.Helper()
	method := "GET"
	if media != "" {
		method = "POST"
	}
	request, err := http.NewRequest(method, origin+path, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if method == "POST" {
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", media)
		request.Header.Set("Accept", accept)
		request.Header.Set("X-Nextgo-Action-Protocol", "1")
		request.Header.Set("X-Nextgo-Action-Revision", "v1")
	}
	for key, values := range headers {
		request.Header[key] = append([]string(nil), values...)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return generatedActionReply{response.StatusCode, response.Header.Clone(), body(t, response)}
}
func TestGeneratedActionsHTTPExecutable(t *testing.T) {
	for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
		t.Run(scope.prefix(), func(t *testing.T) {
			addr := address(t)
			p := start(t, scope.environment(), "-addr", addr)
			defer p.stop(t)
			origin := "http://" + addr
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			defer client.CloseIdleConnections()
			var initial generatedSnapshot
			ready := false
			for deadline := time.Now().Add(7 * time.Second); time.Now().Before(deadline); {
				response, err := client.Get(origin + scope.page())
				if err == nil {
					data := body(t, response)
					if response.StatusCode != 200 {
						t.Fatalf("generated page startup status%d: %s", response.StatusCode, data)
					}
					initial, err = generatedActionSnapshot(data, scope)
					if err != nil {
						t.Fatal(err)
					}
					ready = true
					break
				}
				select {
				case <-p.done:
					t.Fatalf("generated app exited: %v\n%s", p.err, p.logs.String())
				case <-time.After(20 * time.Millisecond):
				}
			}
			if !ready {
				t.Fatal("generated page readiness timeout", p.logs.String())
			}
			if initial.count != 0 || initial.revision != 0 || initial.load < 1 {
				t.Fatal("fresh process demo state", initial)
			}
			warm := generatedActionHTTP(t, client, origin, scope.page(), "", "", "", nil)
			same, err := generatedActionSnapshot(warm.data, scope)
			if err != nil || same != initial {
				t.Fatal("initial page did not reuse its public snapshot", same, initial, err)
			}
			for _, test := range []struct {
				name, payload, revision, protocol, origin, path string
				status                                          int
				code                                            string
				typed                                           bool
			}{
				{name: "stale-revision", payload: `{"amount":1}`, revision: "v0", status: 409, code: "contract_mismatch", typed: true},
				{name: "stale-protocol", payload: `{"amount":1}`, protocol: "99", status: 409, code: "protocol_mismatch", typed: true},
				{name: "malformed", payload: `{`, status: 400, code: "invalid_body", typed: true},
				{name: "unknown-field", payload: `{"amount":1,"unknown":true}`, status: 400, code: "invalid_body", typed: true},
				{name: "trailing-json", payload: `{"amount":1}{}`, status: 400, code: "invalid_body", typed: true},
				{name: "validation", payload: `{"amount":0}`, status: 422, code: "validation_error", typed: true},
				{name: "private-failure", payload: `{"amount":1,"fail":true}`, status: 500, code: "internal_error", typed: true},
				{name: "cross-origin", payload: `{"amount":1}`, origin: "https://untrusted.invalid", status: 403, code: "origin_rejected"},
				{name: "old-endpoint", payload: `{"amount":1}`, path: scope.prefix() + "/_nextgo/actions/generated/add/v0", status: 404, code: "not_found"},
				{name: "oversized", payload: `{"amount":1,"padding":"` + strings.Repeat("x", 1<<20) + `"}`, status: 413, code: "body_too_large", typed: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					headers := make(http.Header)
					if test.revision != "" {
						headers.Set("X-Nextgo-Action-Revision", test.revision)
					}
					if test.protocol != "" {
						headers.Set("X-Nextgo-Action-Protocol", test.protocol)
					}
					if test.origin != "" {
						headers.Set("Origin", test.origin)
					}
					path := scope.endpoint()
					if test.path != "" {
						path = test.path
					}
					reply := generatedActionHTTP(t, client, origin, path, test.payload, "application/json", "application/json", headers)
					if err := generatedActionFailure(reply, test.status, test.code, test.typed); err != nil {
						t.Fatal(err)
					}
				})
			}
			still := generatedActionHTTP(t, client, origin, scope.page(), "", "", "", nil)
			unchanged, err := generatedActionSnapshot(still.data, scope)
			if err != nil || unchanged != initial {
				t.Fatal("failed actions invalidated a confirmed server snapshot", unchanged, initial, err)
			}
			// An authoritative successful confirmation also proves rejected requests did
			// not mutate behind the unchanged cached HTML and hide their side effects.
			added := generatedActionHTTP(t, client, origin, scope.endpoint(), `{"amount":2}`, "application/json", "application/json", nil)
			if err := generatedActionResult(added, 2, 1, scope.page()); err != nil {
				t.Fatal(err)
			}
			freshReply := generatedActionHTTP(t, client, origin, scope.page(), "", "", "", nil)
			fresh, err := generatedActionSnapshot(freshReply.data, scope)
			if err != nil || fresh.count != 2 || fresh.revision != 1 || fresh.load <= initial.load {
				t.Fatal("generated success failed to invalidate SSR data/head", fresh, initial, err)
			}
			native := generatedActionHTTP(t, client, origin, scope.endpoint(), "amount=3", "application/x-www-form-urlencoded", "text/html", nil)
			if err := generatedActionMetadata(native, true); err != nil {
				t.Fatal(err)
			}
			if native.status != 303 || native.headers.Get("Location") != scope.page() {
				t.Fatal("native generated form redirect scope", native.status, native.headers)
			}
			afterReply := generatedActionHTTP(t, client, origin, scope.page(), "", "", "", nil)
			after, err := generatedActionSnapshot(afterReply.data, scope)
			if err != nil || after.count != 5 || after.revision != 2 || after.load <= fresh.load {
				t.Fatal("native form did not publish fresh SSR metadata", after, fresh, err)
			}
			manual := generatedActionHTTP(t, client, origin, scope.prefix()+"/_nextgo/actions/increment", `{"amount":1}`, "application/json", "application/json", http.Header{"X-Nextgo-Action-Revision": {""}})
			var legacy struct {
				Data struct {
					Count int64 `json:"count"`
				} `json:"data"`
			}
			if err := json.Unmarshal(manual.data, &legacy); err != nil || manual.status != 200 || legacy.Data.Count != 1 {
				t.Fatal("automatic registration replaced the app-owned manual router", manual.status, string(manual.data), err)
			}
			denied := generatedActionHTTP(t, client, origin, scope.prefix()+"/_nextgo/actions/review-report", `{}`, "application/json", "application/json", http.Header{"X-Nextgo-Action-Revision": {""}})
			if err := generatedActionFailure(denied, 401, "unauthorized", false); err != nil {
				t.Fatal(err)
			}
			if denied.headers.Get("WWW-Authenticate") != `Bearer realm="nextgo-demo"` {
				t.Fatal("manual router authorization options were lost", denied.headers)
			}
			final := generatedActionHTTP(t, client, origin, scope.page(), "", "", "", nil)
			last, err := generatedActionSnapshot(final.data, scope)
			if err != nil || last != after {
				t.Fatal("unrelated manual action changed generated-action state/cache", last, after, err)
			}
		})
	}
}

type generatedLambdaEvent struct{ method, path, payload, media, accept, revision, protocol, origin string }

func runGeneratedActionLambda(t *testing.T, mode string, scope generatedActionScope, events []generatedLambdaEvent) []generatedActionReply {
	t.Helper()
	var issued, reports atomic.Int32
	done := make(chan struct{})
	results := make(chan generatedActionReply, len(events))
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
			headers := map[string]string{"host": "generated-actions.test"}
			if event.method == "POST" {
				headers["origin"] = "https://generated-actions.test"
				headers["content-type"] = event.media
				headers["accept"] = event.accept
				headers["x-nextgo-action-protocol"] = "1"
				if event.revision != "" {
					headers["x-nextgo-action-revision"] = event.revision
				}
				if event.protocol != "" {
					headers["x-nextgo-action-protocol"] = event.protocol
				}
				if event.origin != "" {
					headers["origin"] = event.origin
				}
			}
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": event.path, "headers": headers, "body": event.payload, "requestContext": map[string]any{"domainName": "generated-actions.test", "http": map[string]string{"method": event.method, "path": event.path}}})
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
				t.Error("generated action Lambda stream metadata missing")
				w.WriteHeader(500)
				return
			}
			if err := json.Unmarshal(data[:split], &envelope); err != nil {
				t.Error(err)
			}
			payload = data[split+8:]
		}
		if int(reports.Add(1)) > len(events) {
			t.Error("unexpected generated action report")
			w.WriteHeader(500)
			return
		}
		headers := make(http.Header)
		for key, value := range envelope.Headers {
			headers.Set(key, value)
		}
		results <- generatedActionReply{envelope.StatusCode, headers, payload}
		w.WriteHeader(202)
	}))
	defer runtime.Close()
	env := append(scope.environment(), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
	p := start(t, env)
	defer closeSeedChild(p)
	select {
	case <-done:
	case <-p.done:
		t.Fatalf("generated action worker exited: %v\n%s", p.err, p.logs.String())
	case <-time.After(20 * time.Second):
		t.Fatal("generated action Lambda flow timeout", p.logs.String())
	}
	p.stop(t)
	if int(reports.Load()) != len(events) {
		t.Fatal("missing generated action Lambda reports", reports.Load(), len(events))
	}
	values := make([]generatedActionReply, len(events))
	for i := range values {
		values[i] = <-results
	}
	return values
}
func TestGeneratedActionsLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				page := func() generatedLambdaEvent { return generatedLambdaEvent{method: "GET", path: scope.page()} }
				action := func(payload string) generatedLambdaEvent {
					return generatedLambdaEvent{method: "POST", path: scope.endpoint(), payload: payload, media: "application/json", accept: "application/json", revision: "v1"}
				}
				stale := action(`{"amount":1}`)
				stale.revision = "v0"
				origin := action(`{"amount":1}`)
				origin.origin = "https://untrusted.invalid"
				form := action("amount=3")
				form.media, form.accept = "application/x-www-form-urlencoded", "text/html"
				manual := action(`{"amount":1}`)
				manual.path = scope.prefix() + "/_nextgo/actions/increment"
				manual.revision = ""
				denied := manual
				denied.path = scope.prefix() + "/_nextgo/actions/review-report"
				denied.payload = `{}`
				events := []generatedLambdaEvent{page(), page(), stale, action(`{"amount":0}`), action(`{"amount":1,"fail":true}`), origin, action(`{`), action(`{"amount":1,"padding":"` + strings.Repeat("x", 1<<20) + `"}`), page(), action(`{"amount":2}`), page(), form, page(), manual, denied, page()}
				replies := runGeneratedActionLambda(t, mode, scope, events)
				initial, err := generatedActionSnapshot(replies[0].data, scope)
				if err != nil || initial.count != 0 || initial.revision != 0 || initial.load < 1 {
					t.Fatal(initial, err)
				}
				for _, index := range []int{1, 8} {
					same, err := generatedActionSnapshot(replies[index].data, scope)
					if err != nil || same != initial {
						t.Fatal("rejected Lambda mutation changed cached snapshot", index, same, initial, err)
					}
				}
				for _, item := range []struct {
					index, status int
					code          string
					typed         bool
				}{{2, 409, "contract_mismatch", true}, {3, 422, "validation_error", true}, {4, 500, "internal_error", true}, {5, 403, "origin_rejected", false}, {6, 400, "invalid_body", true}, {7, 413, "body_too_large", true}, {14, 401, "unauthorized", false}} {
					if err := generatedActionFailure(replies[item.index], item.status, item.code, item.typed); err != nil {
						t.Fatal(item.index, err)
					}
				}
				if err := generatedActionResult(replies[9], 2, 1, scope.page()); err != nil {
					t.Fatal(err)
				}
				fresh, err := generatedActionSnapshot(replies[10].data, scope)
				if err != nil || fresh.count != 2 || fresh.revision != 1 || fresh.load <= initial.load {
					t.Fatal("Lambda generated success did not revalidate public SSR", fresh, initial, err)
				}
				if err := generatedActionMetadata(replies[11], true); err != nil {
					t.Fatal(err)
				}
				if replies[11].status != 303 || replies[11].headers.Get("Location") != scope.page() {
					t.Fatal("Lambda native form redirect lost scope", replies[11].status, replies[11].headers)
				}
				after, err := generatedActionSnapshot(replies[12].data, scope)
				if err != nil || after.count != 5 || after.revision != 2 || after.load <= fresh.load {
					t.Fatal("Lambda native form left stale SSR metadata", after, fresh, err)
				}
				var old struct {
					Data struct {
						Count int64 `json:"count"`
					} `json:"data"`
				}
				if err := json.Unmarshal(replies[13].data, &old); err != nil || replies[13].status != 200 || old.Data.Count != 1 {
					t.Fatal("manual router handler did not coexist", replies[13].status, old, err)
				}
				if replies[14].headers.Get("WWW-Authenticate") != `Bearer realm="nextgo-demo"` {
					t.Fatal("manual authorization challenge was not cloned")
				}
				last, err := generatedActionSnapshot(replies[15].data, scope)
				if err != nil || last != after {
					t.Fatal("unrelated manual actions changed generated snapshot", last, after, err)
				}
			})
		}
	}
}
