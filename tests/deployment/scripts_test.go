//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"html"
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

var scriptNonceRE = regexp.MustCompile(`nonce="([^"]+)"`)

func checkScriptDocument(t *testing.T, status int, headers map[string]string, payload []byte) {
	t.Helper()
	if status != 200 || headers["cache-control"] != "private, no-store" || headers["x-nextgo-nonce"] != "1" || !bytes.Contains(payload, []byte("data-nextgo-script=")) {
		t.Error("script document contract", status, headers)
		return
	}
	nonces := scriptNonceRE.FindAllSubmatch(payload, -1)
	if len(nonces) != 6 {
		t.Error("framework/early/structured-data nonce count", len(nonces))
		return
	}
	nonce := html.UnescapeString(string(nonces[0][1]))
	if !strings.Contains(headers["content-security-policy"], "'nonce-"+nonce+"'") {
		t.Error("policy differs from document nonce")
	}
	for _, value := range nonces {
		if html.UnescapeString(string(value[1])) != nonce {
			t.Error("document contains mixed nonces")
		}
	}
	if bytes.Index(payload, []byte("vendor-early.js")) > bytes.Index(payload, []byte("wasm_exec.js")) {
		t.Error("early script follows runtime")
	}
}
func TestScriptCSPHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, []string{"NEXTGO_DEMO_CSP=true", "NEXTGO_DEMO_EARLY_SCRIPTS=true", "NEXTGO_DEMO_BASE_PATH=/safe", "NEXTGO_DEMO_ASSET_PREFIX=/script-assets"}, "-addr", addr)
	defer p.stop(t)
	client := &http.Client{Timeout: 5 * time.Second}
	var response *http.Response
	for until := time.Now().Add(5 * time.Second); ; {
		var err error
		response, err = client.Get("http://" + addr + "/safe/scripts")
		if err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal(err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	headers := map[string]string{}
	for key, values := range response.Header {
		headers[strings.ToLower(key)] = strings.Join(values, ",")
	}
	checkScriptDocument(t, response.StatusCode, headers, body(t, response))
	request, _ := http.NewRequest("GET", "http://"+addr+"/safe/scripts/details", nil)
	request.Header.Set("X-Nextgo-Navigate", "1")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload := body(t, response)
	if response.Header.Get("X-Nextgo-Nonce") != "1" || !bytes.Contains(payload, []byte("data-nextgo-script=")) {
		t.Fatal("script navigation document missing declaration")
	}
	response, err = client.Get("http://" + addr + "/script-assets/assets/vendor-demo.js")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !bytes.Contains(body(t, response), []byte("NextgoDemoVendor")) {
		t.Fatal("standalone script asset unavailable")
	}
}
func TestScriptCSPLambdaExecutable(t *testing.T) {
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
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "scripts")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/scripts", "headers": map[string]string{"host": "demo.test"}, "requestContext": map[string]any{"http": map[string]string{"method": "GET", "path": "/scripts"}}})
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
				checkScriptDocument(t, response.StatusCode, response.Headers, payload)
				w.WriteHeader(202)
				select {
				case <-done:
				default:
					close(done)
				}
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode, "NEXTGO_DEMO_CSP=true", "NEXTGO_DEMO_EARLY_SCRIPTS=true"})
			defer p.stop(t)
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("script Lambda timeout", p.logs.String())
			}
		})
	}
}

func TestScriptNonceVerifierUnderstandsEscapedAttributeValues(t *testing.T) {
	nonce := "synthetic+nonce0123456789"
	payload := []byte(`<script nonce="synthetic+nonce0123456789" src="vendor-early.js"></script><script nonce="synthetic+nonce0123456789"></script><script nonce="synthetic+nonce0123456789"></script><style nonce="synthetic&#43;nonce0123456789"></style><script nonce="synthetic&#43;nonce0123456789" src="wasm_exec.js"></script><script nonce="synthetic&#43;nonce0123456789"></script><nextgo-script data-nextgo-script="example"></nextgo-script>`)
	checkScriptDocument(t, 200, map[string]string{"cache-control": "private, no-store", "x-nextgo-nonce": "1", "content-security-policy": "script-src 'nonce-" + nonce + "'"}, payload)
}
