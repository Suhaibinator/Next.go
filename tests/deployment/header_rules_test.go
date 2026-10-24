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

func TestHeaderRulesHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, []string{"NEXTGO_DEMO_HEADER_RULES=true", "NEXTGO_DEMO_BASE_PATH=/headers", "NEXTGO_DEMO_ASSET_PREFIX=/header-assets"}, "-addr", addr)
	defer p.stop(t)
	client := &http.Client{Timeout: 5 * time.Second}
	var response *http.Response
	for until := time.Now().Add(5 * time.Second); ; {
		var err error
		response, err = client.Get("http://" + addr + "/headers/rules/headers/article?variant=blue")
		if err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal(err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	payload := body(t, response)
	if response.StatusCode != 200 || response.Header.Get("X-Demo-Stage") != "selected" || response.Header.Get("X-Demo-Slug") != "article" || response.Header.Get("X-Demo-Variant") != "blue" || response.Header.Get("Cache-Control") != "private, no-store" || !bytes.Contains(payload, []byte("Header rules counter")) {
		t.Fatal(response.StatusCode, response.Header)
	}
	request, _ := http.NewRequest("GET", "http://"+addr+"/headers/api/header-policy", nil)
	request.Header.Set("X-Demo-Region", "eu-west")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.Header.Get("X-Demo-Region") != "eu-west" || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(response.Header)
	}
	response, err = client.Get("http://" + addr + "/header-assets/assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 200 || response.Header.Get("X-Demo-Asset") != "style.css" {
		t.Fatal(response.StatusCode, response.Header)
	}
	response, err = client.Get("http://" + addr + "/headers/rules/headers/article?raw=%0D%0AInjected%3Ayes")
	if err != nil {
		t.Fatal(err)
	}
	payload = body(t, response)
	if response.StatusCode != 400 || response.Header.Get("X-Demo-Stage") != "" || bytes.Contains(payload, []byte("Injected")) {
		t.Fatal(response.StatusCode, response.Header, string(payload))
	}
}
func TestHeaderRulesLambdaExecutable(t *testing.T) {
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
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "headers")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/rules/headers/article", "rawQueryString": "variant=green", "headers": map[string]string{"host": "demo.test", "x-nextgo-navigate": "1"}, "requestContext": map[string]any{"http": map[string]string{"method": "GET", "path": "/rules/headers/article"}}})
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
				if response.StatusCode != 200 || response.Headers["x-demo-stage"] != "selected" || response.Headers["x-demo-variant"] != "green" || response.Headers["cache-control"] != "private, no-store" || !bytes.Contains(payload, []byte("Header rules counter")) {
					t.Error("missing conditional Lambda response headers", response.StatusCode, response.Headers)
				}
				w.WriteHeader(202)
				select {
				case <-done:
				default:
					close(done)
				}
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode, "NEXTGO_DEMO_HEADER_RULES=true"})
			defer p.stop(t)
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("header-rule Lambda timeout", p.logs.String())
			}
		})
	}
}
