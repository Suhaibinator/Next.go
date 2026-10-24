//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestComposedParamsHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, nil, "-addr", addr)
	defer p.stop(t)
	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/market/coffee/espresso", "/market/tea/green", "/market/coffee/summary"} {
		var response *http.Response
		for until := time.Now().Add(5 * time.Second); ; {
			var err error
			response, err = client.Get("http://" + addr + path)
			if err == nil {
				break
			}
			if time.Now().After(until) {
				t.Fatal(err, p.logs.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
		payload := body(t, response)
		if response.StatusCode != 200 || response.Header.Get("X-Nextgo-Prerender") != "build" || !bytes.Contains(payload, []byte("data-catalog-category")) {
			t.Fatal("missing composed build snapshot", path, response.StatusCode, response.Header)
		}
	}
	response, err := client.Get("http://" + addr + "/market/coffee/green")
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 404 {
		t.Fatal("parent/child list mixed", response.StatusCode)
	}
}
func TestComposedParamsLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued atomic.Int32
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					if issued.Add(1) > 1 {
						<-r.Context().Done()
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "composed")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/market/coffee/espresso", "headers": map[string]string{"host": "demo.test", "x-nextgo-navigate": "1"}, "requestContext": map[string]any{"http": map[string]string{"method": "GET", "path": "/market/coffee/espresso"}}})
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if err != nil {
					t.Error(err)
				}
				var response struct {
					StatusCode int               `json:"statusCode"`
					Headers    map[string]string `json:"headers"`
					Body       string            `json:"body"`
				}
				var payload []byte
				if mode == "buffered" {
					if err := json.Unmarshal(data, &response); err != nil {
						t.Error(err)
					}
					payload = []byte(response.Body)
				} else {
					split := bytes.Index(data, make([]byte, 8))
					if split < 0 {
						t.Error("missing streaming delimiter")
					} else {
						if err := json.Unmarshal(data[:split], &response); err != nil {
							t.Error(err)
						}
						payload = data[split+8:]
					}
				}
				if response.StatusCode != 200 || response.Headers["x-nextgo-prerender"] != "build" || !bytes.Contains(payload, []byte("coffee / espresso")) {
					t.Error("missing composed Lambda snapshot", response.StatusCode, response.Headers)
				}
				w.WriteHeader(202)
				select {
				case <-done:
				default:
					close(done)
				}
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
			defer p.stop(t)
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("composed Lambda timeout", p.logs.String())
			}
		})
	}
}
