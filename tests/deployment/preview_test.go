//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var previewEnv = []string{"NEXTGO_DEMO_DRAFT_KEY=" + strings.Repeat("d7", 32), "NEXTGO_DEMO_DRAFT_HTTP=true", "NEXTGO_DEMO_ADMIN_TOKEN=" + adminToken}

func TestDraftHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, append(append([]string{}, previewEnv...), "NEXTGO_DEMO_BASE_PATH=/demo"), "-addr", addr)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin := "http://" + addr
	for until := time.Now().Add(5 * time.Second); ; {
		response, err := client.Get(origin + "/demo/preview")
		if err == nil {
			data := body(t, response)
			if response.StatusCode != 200 || !bytes.Contains(data, []byte("Published field note")) {
				t.Fatal(response.StatusCode)
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal(err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, enable := range []bool{true, false} {
		path := "disable"
		if enable {
			path = "enable"
		}
		r, _ := http.NewRequest("POST", origin+"/demo/api/preview/"+path, nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Authorization", "Bearer "+adminToken)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body(t, response)
		if response.StatusCode != 303 || response.Header.Get("Location") != "/demo/preview" {
			t.Fatal(response.StatusCode, response.Header)
		}
		response, err = client.Get(origin + "/demo/preview")
		if err != nil {
			t.Fatal(err)
		}
		data := body(t, response)
		expected, marker := "Published field note", "0"
		if enable {
			expected = "Unpublished field note"
			marker = "1"
			if response.Header.Get("Cache-Control") != "private, no-store" || response.Header.Get("X-Robots-Tag") != "noindex" {
				t.Fatal(response.Header)
			}
		}
		if response.StatusCode != 200 || !bytes.Contains(data, []byte(expected)) || response.Header.Get("X-Nextgo-Draft") != marker {
			t.Fatal(response.StatusCode, response.Header, string(data))
		}
		build, configuration := versionRE.FindSubmatch(data), configurationRE.FindSubmatch(data)
		probe, err := client.Get(origin + "/demo/_nextgo/draft")
		if err != nil {
			t.Fatal(err)
		}
		payload := body(t, probe)
		if len(build) != 2 || len(configuration) != 2 || probe.StatusCode != 204 || len(payload) != 0 || probe.Header.Get("X-Nextgo-Draft") != marker || probe.Header.Get("X-Nextgo-Build") != string(build[1]) || probe.Header.Get("X-Nextgo-Configuration") != string(configuration[1]) || probe.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("restore probe mismatch", probe.StatusCode, probe.Header)
		}
	}
	p.stop(t)
}

func TestDraftLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			var mu sync.Mutex
			cookie := ""
			build, configuration := "", ""
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= 10 {
						if index == 10 {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					path, method := "/preview", "GET"
					headers := map[string]string{"host": "demo.test", "origin": "http://demo.test"}
					if index == 1 || index == 6 {
						path = "/api/preview/enable"
						method = "POST"
						headers["authorization"] = "Bearer " + adminToken
					}
					if index == 4 || index == 8 {
						path = "/api/preview/disable"
						method = "POST"
					}
					if index == 3 {
						headers["x-nextgo-navigate"] = "1"
					}
					if index == 7 || index == 9 {
						path = "/_nextgo/draft"
					}
					mu.Lock()
					session := cookie
					mu.Unlock()
					cookies := []string{}
					if index >= 2 && index <= 4 || index >= 7 && index <= 8 {
						cookies = append(cookies, session)
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": path, "headers": headers, "cookies": cookies, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": method, "path": path, "sourceIp": "127.0.0.1"}}})
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if err != nil {
					t.Error(err)
				}
				var response struct {
					StatusCode int               `json:"statusCode"`
					Headers    map[string]string `json:"headers"`
					Cookies    []string          `json:"cookies"`
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
						w.WriteHeader(500)
						return
					}
					if err := json.Unmarshal(data[:split], &response); err != nil {
						t.Error(err)
					}
					payload = data[split+8:]
				}
				index := int(reports.Add(1)) - 1
				if index == 0 {
					b, c := versionRE.FindSubmatch(payload), configurationRE.FindSubmatch(payload)
					if len(b) != 2 || len(c) != 2 {
						t.Error("missing document generation metadata")
					} else {
						mu.Lock()
						build, configuration = string(b[1]), string(c[1])
						mu.Unlock()
					}
				}
				if index == 1 || index == 4 || index == 6 || index == 8 {
					if response.StatusCode != 303 || len(response.Cookies) != 1 || response.Headers["location"] != "/preview" {
						t.Errorf("cookie step %d: %+v", index, response)
					}
					if (index == 1 || index == 6) && len(response.Cookies) == 1 {
						mu.Lock()
						cookie = strings.SplitN(response.Cookies[0], ";", 2)[0]
						mu.Unlock()
					}
				} else if index == 7 || index == 9 {
					marker := "0"
					if index == 7 {
						marker = "1"
					}
					mu.Lock()
					b, c := build, configuration
					mu.Unlock()
					if response.StatusCode != 204 || len(payload) != 0 || response.Headers["x-nextgo-draft"] != marker || response.Headers["x-nextgo-build"] != b || response.Headers["x-nextgo-configuration"] != c || response.Headers["x-nextgo-protocol"] != "3" || response.Headers["cache-control"] != "private, no-store" {
						t.Error("Lambda restore probe mismatch", index, response.StatusCode, response.Headers)
					}
				} else {
					expected, marker := "Published field note", "0"
					if index == 2 || index == 3 {
						expected = "Unpublished field note"
						marker = "1"
						if response.Headers["cache-control"] != "private, no-store" || response.Headers["x-robots-tag"] != "noindex" {
							t.Error("private preview headers missing")
						}
					}
					if response.StatusCode != 200 || response.Headers["x-nextgo-draft"] != marker || !bytes.Contains(payload, []byte(expected)) {
						t.Errorf("preview step %d: status %d headers %v", index, response.StatusCode, response.Headers)
					}
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			env := append(append([]string{}, previewEnv...), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
			p := start(t, env)
			select {
			case <-done:
			case <-p.done:
				t.Fatal(p.logs.String())
			case <-time.After(8 * time.Second):
				t.Fatal("preview lambda fixture timed out", p.logs.String())
			}
			p.stop(t)
			if reports.Load() != 10 {
				t.Fatal("missing preview responses", reports.Load())
			}
		})
	}
}
