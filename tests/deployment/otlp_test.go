//go:build deployment

package deployment

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOTLPLambdaExecutableFlushesCompletionBeforeReuse(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			names := map[string]bool{}
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Resources []struct {
						Scopes []struct {
							Spans []struct {
								Name    string `json:"name"`
								TraceID string `json:"traceId"`
							} `json:"spans"`
						} `json:"scopeSpans"`
					} `json:"resourceSpans"`
				}
				if r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid OTLP request", r.URL)
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				mu.Lock()
				for _, resource := range request.Resources {
					for _, scope := range resource.Scopes {
						for _, span := range scope.Spans {
							names[span.Name] = true
							if len(span.TraceID) != 32 {
								t.Error(span)
							}
						}
					}
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, "{}")
			}))
			defer collector.Close()
			nextSeen := make(chan struct{})
			var nexts, reports atomic.Int32
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					if nexts.Add(1) > 1 {
						mu.Lock()
						if !names["nextgo.after"] || !names["nextgo.request"] {
							t.Error("next invocation fetched before OTLP completion flush", names)
						}
						mu.Unlock()
						if nexts.Load() == 2 {
							close(nextSeen)
						}
						<-r.Context().Done()
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "otlp")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(10*time.Second).UnixMilli(), 10))
					_, _ = io.WriteString(w, `{"version":"2.0","rawPath":"/lifecycle","rawQueryString":"secret=private-otlp-probe","headers":{"host":"demo.test"},"requestContext":{"domainName":"demo.test","http":{"method":"GET","path":"/lifecycle","protocol":"HTTP/1.1","sourceIp":"127.0.0.1"}}}`)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				reports.Add(1)
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode, "NEXTGO_DEMO_OTLP_ENDPOINT=" + collector.URL + "/v1/traces"})
			select {
			case <-nextSeen:
			case <-p.done:
				t.Fatal("OTLP runtime stopped before flush", p.logs.String())
			case <-time.After(8 * time.Second):
				t.Fatal("OTLP runtime did not drain", p.logs.String())
			}
			p.stop(t)
			if reports.Load() != 1 {
				t.Fatal("response duplicated", reports.Load())
			}
		})
	}
}
