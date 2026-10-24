//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	htmlpkg "html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func interceptionCarrier(t *testing.T, content []byte, configuration string) string {
	t.Helper()
	if configuration == "" {
		match := configurationRE.FindSubmatch(content)
		if len(match) != 2 {
			t.Fatal("missing interception configuration")
		}
		configuration = string(match[1])
	}
	selections := map[string]string{}
	attribute := regexp.MustCompile(`data-nextgo-slot-url="([^"]*)"`)
	for _, marker := range parallelMarker.FindAll(content, -1) {
		key, url := parallelKey.FindSubmatch(marker), attribute.FindSubmatch(marker)
		if len(key) == 2 && len(url) == 2 {
			selections[string(key[1])] = htmlpkg.UnescapeString(string(url[1]))
		}
	}
	if len(selections) != 2 {
		t.Fatal("expected primary and modal selections", len(selections))
	}
	encoded, _ := json.Marshal(map[string]any{"configuration": configuration, "selections": selections})
	return string(encoded)
}

func checkInterception(t *testing.T, index, status int, content []byte) string {
	t.Helper()
	configuration := ""
	if index > 0 && index != 3 {
		var nav struct{ HTML, Configuration, URL string }
		if err := json.Unmarshal(content, &nav); err != nil {
			t.Error(err)
			return ""
		}
		configuration = nav.Configuration
		content = []byte(nav.HTML)
		if index == 1 && nav.URL != "/photos/one" {
			t.Error("intercepted address changed to background", nav.URL)
		}
	}
	switch index {
	case 0, 2:
		if status != 200 || !bytes.Contains(content, []byte("Photo gallery")) || bytes.Contains(content, []byte("<dialog")) {
			t.Error("invalid gallery response", index, status)
		}
	case 1, 6, 7, 8:
		if status != 200 || !bytes.Contains(content, []byte("Photo gallery")) || !bytes.Contains(content, []byte("Coastal light")) || !bytes.Contains(content, []byte("<dialog")) {
			t.Error("invalid contextual modal", status)
		}
		marker := regexp.MustCompile(`<[^>]*data-nextgo-island="modal"[^>]*>`).Find(content)
		props := regexp.MustCompile(`data-nextgo-props="([^"]*)"`).FindSubmatch(marker)
		var origin struct{ ReturnTo string }
		if len(props) != 2 || json.Unmarshal([]byte(htmlpkg.UnescapeString(string(props[1]))), &origin) != nil || origin.ReturnTo != "/gallery" {
			t.Error("interception did not publish its scoped return target")
		}
		if index == 7 {
			retained := false
			for _, marker := range parallelMarker.FindAll(content, -1) {
				if bytes.Contains(marker, []byte(`data-nextgo-slot-name="modal"`)) && bytes.Contains(marker, []byte(`data-nextgo-slot-retain="true"`)) {
					retained = true
				}
			}
			if !retained {
				t.Error("active modal refresh lost its branch selection")
			}
		}
		if index == 8 && !bytes.Contains(content, []byte("Updated on the server with the current request.")) {
			t.Error("modal refresh did not load current server output")
		}
	case 3:
		if status != 200 || !bytes.Contains(content, []byte("Standalone photo page")) || bytes.Contains(content, []byte("<dialog")) {
			t.Error("direct photo was intercepted", status)
		}
	case 4, 5, 9, 10:
		if status != 403 || !bytes.Contains(content, []byte("Access denied")) || bytes.Contains(content, []byte("Gallery counter")) || bytes.Contains(content, []byte("<dialog")) {
			t.Error("interception denial leaked background or modal", index, status)
		}
	}
	if index == 0 || index == 1 || index == 2 || index == 6 || index == 7 || index == 8 {
		return interceptionCarrier(t, content, configuration)
	}
	return ""
}

var interceptionPaths = []string{"/gallery", "/photos/one", "/gallery", "/photos/one", "/photos/one", "/photos/one", "/photos/one", "/photos/one", "/photos/one", "/photos/one", "/photos/one"}

func TestInterceptionHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, nil, "-addr", addr)
	client := &http.Client{Timeout: 3 * time.Second}
	carrier := ""
	for index, path := range interceptionPaths {
		request, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		if index > 0 && index != 3 {
			request.Header.Set("X-Nextgo-Navigate", "1")
			request.Header.Set("X-Nextgo-Slots", carrier)
		}
		if index == 4 || index == 9 {
			request.Header.Set("X-Demo-Gallery-Deny", "yes")
		}
		if index == 5 || index == 10 {
			request.Header.Set("X-Demo-Photo-Deny", "yes")
		}
		if index == 8 {
			request.Header.Set("X-Demo-Photo-Version", "updated")
		}
		var response *http.Response
		for until := time.Now().Add(5 * time.Second); ; {
			var err error
			response, err = client.Do(request)
			if err == nil {
				break
			}
			if time.Now().After(until) {
				t.Fatal(err, p.logs.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
		if next := checkInterception(t, index, response.StatusCode, body(t, response)); next != "" {
			carrier = next
		}
	}
	p.stop(t)
}

func TestInterceptionLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			var mu sync.Mutex
			carrier := ""
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= len(interceptionPaths) {
						if index == len(interceptionPaths) {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					path := interceptionPaths[index]
					headers := map[string]string{"host": "demo.test"}
					if index > 0 && index != 3 {
						headers["x-nextgo-navigate"] = "1"
						mu.Lock()
						headers["x-nextgo-slots"] = carrier
						mu.Unlock()
					}
					if index == 4 || index == 9 {
						headers["x-demo-gallery-deny"] = "yes"
					}
					if index == 5 || index == 10 {
						headers["x-demo-photo-deny"] = "yes"
					}
					if index == 8 {
						headers["x-demo-photo-version"] = "updated"
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": path, "headers": headers, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": "GET", "path": path, "sourceIp": "127.0.0.1"}}})
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
				} else {
					split := bytes.Index(data, make([]byte, 8))
					if split < 0 {
						t.Error("missing Lambda delimiter")
						w.WriteHeader(500)
						return
					}
					if err := json.Unmarshal(data[:split], &envelope); err != nil {
						t.Error(err)
					}
					payload = data[split+8:]
				}
				index := int(reports.Add(1)) - 1
				if next := checkInterception(t, index, envelope.StatusCode, payload); next != "" {
					mu.Lock()
					carrier = next
					mu.Unlock()
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
			select {
			case <-done:
			case <-p.done:
				t.Fatal(p.logs.String())
			case <-time.After(8 * time.Second):
				t.Fatal("interception runtime did not finish", p.logs.String())
			}
			p.stop(t)
			if reports.Load() != int32(len(interceptionPaths)) {
				t.Fatal("missing interception reports", reports.Load())
			}
		})
	}
}
