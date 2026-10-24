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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func ruleUpstream(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		data, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Nextgo-Protocol", "3")
		w.Header().Set("X-Nextgo-Configuration", strings.Repeat("a", 64))
		w.Header().Set("Set-Cookie", "upstream=blocked")
		_ = json.NewEncoder(w).Encode(map[string]string{"method": r.Method, "uri": r.URL.RequestURI(), "body": string(data), "cookie": r.Header.Get("Cookie"), "authorization": r.Header.Get("Authorization"), "navigate": r.Header.Get("X-Nextgo-Navigate")})
	}))
}
func checkRuleProxyResult(t *testing.T, status int, headers map[string]string, payload []byte) {
	t.Helper()
	var result map[string]string
	if status != 200 || json.Unmarshal(payload, &result) != nil || result["method"] != "POST" || result["uri"] != "/echo?q=x%26y" || result["body"] != "Go proxy body" || result["cookie"] != "" || result["authorization"] != "" || result["navigate"] != "" {
		t.Error("invalid executable proxy result", status, string(payload))
	}
	if headers["x-nextgo-protocol"] != "" || headers["x-nextgo-configuration"] != "" || headers["set-cookie"] != "" {
		t.Error("proxy exposed ownership or cookie headers", headers)
	}
}
func TestRequestRulesHTTPExecutable(t *testing.T) {
	upstream := ruleUpstream(t)
	defer upstream.Close()
	addr := address(t)
	origin := "http://" + addr
	p := start(t, []string{"NEXTGO_DEMO_RULES=true", "NEXTGO_DEMO_PROXY_ORIGIN=" + upstream.URL, "NEXTGO_DEMO_BASE_PATH=/policy", "NEXTGO_DEMO_PROXY_TIMEOUT=100ms"}, "-addr", addr)
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for until := time.Now().Add(5 * time.Second); ; {
		response, err := client.Get(origin + "/policy/rules/choose?post=html-first")
		if err == nil {
			data := body(t, response)
			if response.StatusCode != 200 || !bytes.Contains(data, []byte("Start with the HTML")) || response.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal(response.StatusCode, string(data))
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal(err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	r, _ := http.NewRequest("POST", origin+"/policy/rules/remote/echo?q=x%26y", strings.NewReader("Go proxy body"))
	r.Header.Set("Cookie", "private=secret")
	r.Header.Set("Authorization", "Bearer private")
	r.Header.Set("X-Nextgo-Navigate", "1")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	for key := range response.Header {
		headers[strings.ToLower(key)] = response.Header.Get(key)
	}
	checkRuleProxyResult(t, response.StatusCode, headers, body(t, response))
	p.stop(t)
}
func TestRequestRulesLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			upstream := ruleUpstream(t)
			defer upstream.Close()
			paths := []string{"/rules/choose", "/rules/jump/html-first", "/rules/remote/echo", "/rules/remote/slow"}
			queries := []string{"post=html-first", "go=yes", "q=x%26y", ""}
			var issued, reports atomic.Int32
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= len(paths) {
						if index == len(paths) {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					method, body := "GET", ""
					if index == 2 {
						method, body = "POST", "Go proxy body"
					}
					headers := map[string]string{"host": "demo.test"}
					if index == 2 {
						headers["cookie"] = "private=secret"
						headers["authorization"] = "Bearer private"
						headers["x-nextgo-navigate"] = "1"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/policy" + paths[index], "rawQueryString": queries[index], "body": body, "headers": headers, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": method, "path": "/policy" + paths[index], "sourceIp": "127.0.0.1"}}})
					return
				}
				parts := strings.Split(r.URL.Path, "/")
				if len(parts) < 3 || parts[len(parts)-1] != "response" {
					t.Error("unexpected runtime report")
					w.WriteHeader(500)
					return
				}
				index, err := strconv.Atoi(parts[len(parts)-2])
				if err != nil || index < 0 || index >= len(paths) {
					t.Error("invalid report id")
					w.WriteHeader(500)
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
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
					separator := bytes.Index(data, make([]byte, 8))
					if separator < 0 {
						t.Error("missing stream delimiter")
						w.WriteHeader(500)
						return
					}
					if err := json.Unmarshal(data[:separator], &envelope); err != nil {
						t.Error(err)
					}
					payload = data[separator+8:]
				}
				switch index {
				case 0:
					if envelope.StatusCode != 200 || !bytes.Contains(payload, []byte("Start with the HTML")) || envelope.Headers["cache-control"] != "private, no-store" {
						t.Error("conditional Lambda page", envelope.StatusCode, string(payload))
					}
				case 1:
					if envelope.StatusCode != 307 || envelope.Headers["location"] != "/policy/posts/html-first?go=yes" {
						t.Error("conditional Lambda redirect", envelope)
					}
				case 2:
					checkRuleProxyResult(t, envelope.StatusCode, envelope.Headers, payload)
				case 3:
					if envelope.StatusCode != 504 || string(payload) != "Upstream request failed\n" {
						t.Error("Lambda proxy timeout", envelope.StatusCode, string(payload))
					}
				}
				reports.Add(1)
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode, "NEXTGO_DEMO_RULES=true", "NEXTGO_DEMO_PROXY_ORIGIN=" + upstream.URL, "NEXTGO_DEMO_BASE_PATH=/policy", "NEXTGO_DEMO_PROXY_TIMEOUT=100ms"})
			select {
			case <-done:
			case <-p.done:
				t.Fatal("runtime exited", p.logs.String())
			case <-time.After(8 * time.Second):
				t.Fatal("rule runtime did not finish", p.logs.String())
			}
			p.stop(t)
			if reports.Load() != int32(len(paths)) {
				t.Fatal(fmt.Sprint("missing reports: ", reports.Load()))
			}
		})
	}
}
