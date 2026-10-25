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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

const actionViewMedia = "application/x-nextgo-action-view+json"

type actionViewEvent struct {
	method, path, payload string
	headers               http.Header
}

type actionViewSnapshot struct{ count, revision, load int64 }

type actionViewDeploymentFrame struct {
	Version int             `json:"version"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Page    json.RawMessage `json:"page"`
	ID      string          `json:"id"`
	HTML    string          `json:"html"`
	Failed  bool            `json:"failed"`
}

func actionViewSnapshotFrom(data []byte) (actionViewSnapshot, error) {
	var snapshot actionViewSnapshot
	for _, field := range []struct {
		name  string
		value *int64
	}{{"count", &snapshot.count}, {"revision", &snapshot.revision}, {"load", &snapshot.load}} {
		match := regexp.MustCompile(`<output[^>]*\bdata-action-view-ssr-` + field.name + `=""[^>]*>([0-9]+)</output>`).FindSubmatch(data)
		if len(match) != 2 {
			return snapshot, fmt.Errorf("missing action-view SSR %s", field.name)
		}
		value, err := strconv.ParseInt(string(match[1]), 10, 64)
		if err != nil {
			return snapshot, err
		}
		*field.value = value
	}
	return snapshot, nil
}

// Exercise the same public protocol against a socket server and each actual
// Lambda adapter. Only the first GET supplies the current document identity;
// the successful POST must itself carry both confirmation and refreshed view.
func exerciseActionView(t *testing.T, scope generatedActionScope, origin string, send func(actionViewEvent) generatedActionReply) {
	t.Helper()
	page := scope.prefix() + "/action-view"
	if scope.slash {
		page += "/"
	}
	source := page + "?delay=1ms&from=combined"
	endpoint := scope.prefix() + "/_nextgo/actions/view/save/v1"
	initial := send(actionViewEvent{method: "GET", path: source})
	before, err := actionViewSnapshotFrom(initial.data)
	if err != nil || initial.status != 200 || before.count != 0 || before.revision != 0 || before.load < 1 {
		t.Fatalf("initial action-view SSR: %d %+v %v %s", initial.status, before, err, initial.data)
	}
	build := regexp.MustCompile(`data-nextgo-build="([^"]+)"`).FindSubmatch(initial.data)
	configuration := configurationRE.FindSubmatch(initial.data)
	if len(build) != 2 || len(configuration) != 2 || initial.headers.Get("X-Nextgo-Configuration") != string(configuration[1]) || !bytes.Contains(initial.data, []byte("<title>Action view revision 0 — Next.go</title>")) || !bytes.Contains(initial.data, []byte(`action="`+endpoint+`"`)) {
		t.Fatal("initial action-view document identity/title/native form missing", initial.headers, string(initial.data))
	}
	headers := http.Header{
		"Origin": {origin}, "Content-Type": {"application/json"}, "Accept": {actionViewMedia},
		"X-Nextgo-Action-Protocol": {"1"}, "X-Nextgo-Action-Revision": {"v1"},
		"X-Nextgo-Action-View": {"1"}, "X-Nextgo-Action-Url": {source},
		"X-Nextgo-Action-Build": {string(build[1])}, "X-Nextgo-Configuration": {string(configuration[1])},
	}
	post := func(payload string, overrides http.Header) generatedActionReply {
		h := headers.Clone()
		for name, values := range overrides {
			if len(values) == 0 {
				h.Del(name)
				continue
			}
			h[name] = append([]string(nil), values...)
		}
		return send(actionViewEvent{method: "POST", path: endpoint, payload: payload, headers: h})
	}
	for _, test := range []struct {
		name, header, value string
		status              int
	}{
		{"old-build", "X-Nextgo-Action-Build", "old-build", 409},
		{"old-configuration", "X-Nextgo-Configuration", strings.Repeat("a", 64), 409},
		{"wrong-draft", "X-Nextgo-Draft", "1", 409},
		{"API-source", "X-Nextgo-Action-Url", scope.prefix() + "/api/health", 400},
		{"bad-slots", "X-Nextgo-Slots", `{}`, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := make(http.Header)
			h.Set(test.header, test.value)
			reply := post(`{"amount":1}`, h)
			if err := generatedActionFailure(reply, test.status, "view_unavailable", true); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(reply.headers.Get("Content-Type"), "application/json") || reply.headers.Get("X-Nextgo-Action-View") != "" {
				t.Fatal("preflight rejection incorrectly started a confirmed stream", reply.headers)
			}
		})
	}
	for _, test := range []struct {
		payload, code string
		status        int
	}{{`{"amount":0}`, "validation_error", 422}, {`{"amount":1,"fail":true}`, "internal_error", 500}, {`{`, "invalid_body", 400}} {
		reply := post(test.payload, nil)
		if err := generatedActionFailure(reply, test.status, test.code, true); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(reply.headers.Get("Content-Type"), "application/json") || bytes.Contains(reply.data, []byte("private")) {
			t.Fatal("unconfirmed failure changed representation or exposed handler detail", string(reply.data))
		}
	}
	// An exact authoritative count also detects hidden mutations that a cached
	// unchanged GET could conceal after a rejected request.
	fresh := checkActionViewStream(t, post(`{"amount":2}`, nil), string(build[1]), string(configuration[1]), source, 2, 1, false)
	if fresh.load <= before.load {
		t.Fatal("combined response reused the pre-mutation SSR snapshot", before, fresh)
	}
	failed := checkActionViewStream(t, post(`{"amount":1,"viewFail":true}`, nil), string(build[1]), string(configuration[1]), source, 3, 2, true)
	if failed.load <= fresh.load {
		t.Fatal("confirmed late failure did not render a fresh shell", fresh, failed)
	}
	// Recovery is another explicit mutation, never an automatic retry of the
	// already confirmed amount=1 operation whose deferred view failed.
	recovered := checkActionViewStream(t, post(`{"amount":1}`, nil), string(build[1]), string(configuration[1]), source, 4, 3, false)
	if recovered.load <= failed.load {
		t.Fatal("view recovery reused failed shell", failed, recovered)
	}
	legacy := post(`{"amount":1}`, http.Header{"Accept": {"application/json"}, "X-Nextgo-Action-View": nil})
	if err := generatedActionResult(legacy, 5, 4, ""); err != nil {
		t.Fatal("ordinary typed action no longer coexists", err)
	}
}

func checkActionViewStream(t *testing.T, reply generatedActionReply, build, configuration, source string, count, revision int64, failed bool) actionViewSnapshot {
	t.Helper()
	if err := generatedActionMetadata(reply, true); err != nil {
		t.Fatal(err)
	}
	if reply.status != 200 || !strings.HasPrefix(reply.headers.Get("Content-Type"), actionViewMedia) || reply.headers.Get("Cache-Control") != "private, no-store" || reply.headers.Get("X-Nextgo-Action-View") != "1" || reply.headers.Get("X-Nextgo-Configuration") != configuration {
		t.Fatalf("combined action stream headers/status: %d %v %s", reply.status, reply.headers, reply.data)
	}
	var frames []actionViewDeploymentFrame
	for _, line := range bytes.Split(reply.data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var frame actionViewDeploymentFrame
		if err := json.Unmarshal(line, &frame); err != nil || frame.Version != 1 {
			t.Fatalf("invalid combined frame: %s %v", line, err)
		}
		frames = append(frames, frame)
	}
	if len(frames) != 4 || frames[0].Type != "result" || frames[1].Type != "shell" || frames[2].Type != "chunk" || frames[3].Type != "done" {
		t.Fatal("confirmation/shell/boundary/completion order missing", string(reply.data))
	}
	var result generatedActionBody
	if err := json.Unmarshal(frames[0].Result, &result); err != nil || result.Data == nil || result.Data.Count != count || result.Data.Revision != revision || result.Redirect != "" || result.Error != nil {
		t.Fatal("combined typed result mismatch", result, err)
	}
	var page struct {
		Version, Status                        int
		Build, Configuration, URL, HTML, Title string
		Meta                                   map[string]string
	}
	if err := json.Unmarshal(frames[1].Page, &page); err != nil || page.Version != navigation.ProtocolVersion || page.Status != 200 || page.Build != build || page.Configuration != configuration || page.URL != source || page.Title != fmt.Sprintf("Action view revision %d — Next.go", revision) || page.Meta["action-view-revision"] != strconv.FormatInt(revision, 10) {
		t.Fatal("fresh combined view metadata mismatch", page, err)
	}
	snapshot, err := actionViewSnapshotFrom([]byte(page.HTML))
	if err != nil || snapshot.count != count || snapshot.revision != revision || !strings.Contains(page.HTML, "data-action-view-fallback") || !strings.Contains(page.HTML, fmt.Sprintf(`data-av-count="">%d</output>`, count)) || !strings.Contains(page.HTML, fmt.Sprintf(`data-av-revision="">%d</output>`, revision)) || frames[2].ID != "action-view-detail" || frames[2].Failed != failed {
		t.Fatal("fresh combined SSR/deferred boundary mismatch", snapshot, err, string(reply.data))
	}
	if failed {
		if strings.Contains(frames[2].HTML, "private") || strings.Contains(frames[2].HTML, "data-action-view-deferred") {
			t.Fatal("failed region exposed private detail or masqueraded as success", frames[2].HTML)
		}
	} else if !strings.Contains(frames[2].HTML, fmt.Sprintf(`data-action-view-deferred="">%d</output>`, count)) || !strings.Contains(frames[2].HTML, fmt.Sprintf(`data-action-view-deferred-revision="">%d</output>`, revision)) {
		t.Fatal("combined response omitted fresh deferred region", frames[2].HTML)
	}
	return snapshot
}

func TestActionViewHTTPExecutable(t *testing.T) {
	for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
		t.Run(scope.prefix(), func(t *testing.T) {
			addr := address(t)
			p := start(t, scope.environment(), "-addr", addr)
			defer p.stop(t)
			client := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			defer client.CloseIdleConnections()
			origin := "http://" + addr
			health := scope.prefix() + "/api/health"
			if scope.slash {
				health += "/"
			}
			for deadline := time.Now().Add(7 * time.Second); ; {
				response, err := client.Get(origin + health)
				if err == nil {
					_ = body(t, response)
					if response.StatusCode == 200 {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("action-view server readiness timeout", p.logs.String())
				}
				time.Sleep(20 * time.Millisecond)
			}
			exerciseActionView(t, scope, origin, func(event actionViewEvent) generatedActionReply {
				r, err := http.NewRequest(event.method, origin+event.path, strings.NewReader(event.payload))
				if err != nil {
					t.Fatal(err)
				}
				r.Header = event.headers.Clone()
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				return generatedActionReply{response.StatusCode, response.Header.Clone(), body(t, response)}
			})
		})
	}
}

func TestActionViewLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				events := make(chan actionViewEvent)
				replies := make(chan generatedActionReply, 1)
				var issued atomic.Int32
				runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/next") {
						var event actionViewEvent
						select {
						case event = <-events:
						case <-r.Context().Done():
							return
						}
						u, _ := url.Parse(event.path)
						headers := map[string]string{"host": "action-view.test"}
						for name, values := range event.headers {
							headers[strings.ToLower(name)] = strings.Join(values, ",")
						}
						w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(int(issued.Add(1))))
						w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(8*time.Second).UnixMilli(), 10))
						_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": u.Path, "rawQueryString": u.RawQuery, "headers": headers, "body": event.payload, "requestContext": map[string]any{"domainName": "action-view.test", "http": map[string]string{"method": event.method, "path": u.Path}}})
						return
					}
					data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
					if err != nil {
						t.Error(err)
					}
					var envelope httpEnvelope
					var payload []byte
					if mode == "buffered" {
						err = json.Unmarshal(data, &envelope)
						payload = []byte(envelope.Body)
						if envelope.Base64 {
							payload, err = base64.StdEncoding.DecodeString(envelope.Body)
						}
					} else if split := bytes.Index(data, make([]byte, 8)); split >= 0 {
						err = json.Unmarshal(data[:split], &envelope)
						payload = data[split+8:]
					} else {
						err = fmt.Errorf("missing Lambda streaming metadata: %s", data)
					}
					if err != nil {
						t.Error(err)
					}
					headers := make(http.Header)
					for name, value := range envelope.Headers {
						headers.Set(name, value)
					}
					replies <- generatedActionReply{envelope.StatusCode, headers, payload}
					w.WriteHeader(202)
				}))
				defer runtime.Close()
				p := start(t, append(scope.environment(), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode))
				defer closeSeedChild(p)
				exerciseActionView(t, scope, "https://action-view.test", func(event actionViewEvent) generatedActionReply {
					select {
					case events <- event:
					case <-p.done:
						t.Fatal("action-view worker exited", p.err, p.logs.String())
					case <-time.After(10 * time.Second):
						t.Fatal("action-view worker did not request next invocation", p.logs.String())
					}
					select {
					case reply := <-replies:
						return reply
					case <-p.done:
						t.Fatal("action-view worker exited without response", p.err, p.logs.String())
					case <-time.After(10 * time.Second):
						t.Fatal("action-view Lambda response timeout", p.logs.String())
					}
					return generatedActionReply{}
				})
				p.stop(t)
			})
		}
	}
}
