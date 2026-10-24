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

var parallelMarker = regexp.MustCompile(`<nextgo-slot [^>]*>`)
var parallelKey = regexp.MustCompile(`data-nextgo-slot="([a-f0-9]{64})"`)

func parallelCarrier(t *testing.T, html []byte) string {
	t.Helper()
	configuration := configurationRE.FindSubmatch(html)
	if len(configuration) != 2 {
		t.Fatal("missing slot configuration")
	}
	return parallelCarrierContent(t, html, string(configuration[1]))
}
func parallelCarrierContent(t *testing.T, html []byte, configuration string) string {
	t.Helper()
	selections := map[string]string{}
	urlAttribute := regexp.MustCompile(`data-nextgo-slot-url="([^"]*)"`)
	hasAnalytics, hasPrimary := false, false
	for _, marker := range parallelMarker.FindAll(html, -1) {
		key, url := parallelKey.FindSubmatch(marker), urlAttribute.FindSubmatch(marker)
		if len(key) == 2 && len(url) == 2 {
			selections[string(key[1])] = htmlpkg.UnescapeString(string(url[1]))
		}
		hasAnalytics = hasAnalytics || bytes.Contains(marker, []byte(`data-nextgo-slot-name="analytics"`))
		hasPrimary = hasPrimary || bytes.Contains(marker, []byte(`data-nextgo-slot-name="children"`))
	}
	if !hasAnalytics || !hasPrimary {
		t.Fatal("missing primary/analytics marker")
	}
	encoded, _ := json.Marshal(map[string]any{"configuration": configuration, "selections": selections})
	return string(encoded)
}
func TestParallelHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, nil, "-addr", addr)
	client := &http.Client{Timeout: 3 * time.Second}
	origin := "http://" + addr
	var chart []byte
	for until := time.Now().Add(5 * time.Second); ; {
		response, err := client.Get(origin + "/boards/chart")
		if err == nil {
			chart = body(t, response)
			if response.StatusCode != 200 || !bytes.Contains(chart, []byte("Chart analytics")) {
				t.Fatal(response.StatusCode, string(chart))
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal(err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	response, err := client.Get(origin + "/boards/settings")
	if err != nil {
		t.Fatal(err)
	}
	direct := body(t, response)
	if response.StatusCode != 200 || (!bytes.Contains(direct, []byte("Analytics default")) || !bytes.Contains(direct, []byte("data-analytics-shell"))) || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(response.StatusCode, string(direct))
	}

	previousState := ""
	for _, tc := range defaultBranchCases {
		path := tc.path
		if path == "" {
			path = "/boards/settings"
		}
		req, _ := http.NewRequest("GET", origin+path, nil)
		if tc.header != "" {
			req.Header.Set(tc.header, tc.value)
		}
		if tc.retain {
			req.Header.Set("X-Nextgo-Navigate", "1")
			req.Header.Set("X-Nextgo-Slots", previousState)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		content := body(t, res)
		if tc.retain {
			var nav struct {
				HTML          string `json:"html"`
				Configuration string `json:"configuration"`
			}
			if err := json.Unmarshal(content, &nav); err != nil {
				t.Fatal(err)
			}
			content = []byte(nav.HTML)
			if res.StatusCode == 200 {
				previousState = parallelCarrierContent(t, content, nav.Configuration)
			}
		} else if res.StatusCode == 200 {
			previousState = parallelCarrier(t, content)
		}
		checkDefaultBranch(t, tc, res.StatusCode, string(content))
	}
	carrier := parallelCarrier(t, chart)
	for _, denied := range []bool{false, true} {
		request, _ := http.NewRequest("GET", origin+"/boards/activity", nil)
		request.Header.Set("X-Nextgo-Navigate", "1")
		request.Header.Set("X-Nextgo-Slots", carrier)
		if denied {
			request.Header.Set("X-Demo-Slot-Deny", "yes")
		}
		response, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			HTML string `json:"html"`
		}
		data := body(t, response)
		if json.Unmarshal(data, &envelope) != nil {
			t.Fatal(string(data))
		}
		if denied {
			if response.StatusCode != 403 || strings.Contains(envelope.HTML, "Chart analytics") {
				t.Fatal(response.StatusCode, envelope.HTML)
			}
		} else if response.StatusCode != 200 || !strings.Contains(envelope.HTML, `data-nextgo-slot-retain="true"`) || (!strings.Contains(envelope.HTML, "Chart analytics") || !strings.Contains(envelope.HTML, "Primary chart page") || !strings.Contains(envelope.HTML, "Team activity")) {
			t.Fatal(response.StatusCode, envelope.HTML)
		}
	}
	p.stop(t)
}
func TestParallelLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			done := make(chan struct{})
			carrier := ""
			var carrierMu sync.Mutex
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := issued.Add(1) - 1
					if index >= 3+int32(len(defaultBranchCases)) {
						if index == 3+int32(len(defaultBranchCases)) {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					path := "/boards/chart"
					headers := map[string]string{"host": "demo.test"}
					if index > 0 {
						path = "/boards/activity"
						headers["x-nextgo-navigate"] = "1"
						carrierMu.Lock()
						headers["x-nextgo-slots"] = carrier
						carrierMu.Unlock()
					}
					if index == 2 {
						headers["x-demo-slot-deny"] = "yes"
					}
					if index >= 3 {
						path = "/boards/settings"
						if defaultBranchCases[index-3].path != "" {
							path = defaultBranchCases[index-3].path
						}
						tc := defaultBranchCases[index-3]
						if !tc.retain {
							delete(headers, "x-nextgo-slots")
						}
						if tc.header != "" {
							headers[strings.ToLower(tc.header)] = tc.value
						}
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(int(index)))
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
				index := reports.Add(1) - 1
				if index == 0 {
					carrierMu.Lock()
					carrier = parallelCarrier(t, payload)
					carrierMu.Unlock()
					if envelope.StatusCode != 200 {
						t.Error(envelope.StatusCode)
					}
				} else {
					var nav struct {
						HTML          string `json:"html"`
						Configuration string `json:"configuration"`
					}
					if err := json.Unmarshal(payload, &nav); err != nil {
						t.Error(err)
					}
					if index == 1 {
						if envelope.StatusCode != 200 || !strings.Contains(nav.HTML, `data-nextgo-slot-retain="true"`) || (!strings.Contains(nav.HTML, "Chart analytics") || !strings.Contains(nav.HTML, "Primary chart page") || !strings.Contains(nav.HTML, "Team activity")) {
							t.Error(envelope.StatusCode, nav.HTML)
						}
					} else if index >= 3 {
						if envelope.StatusCode == 200 {
							carrierMu.Lock()
							carrier = parallelCarrierContent(t, []byte(nav.HTML), nav.Configuration)
							carrierMu.Unlock()
						}
						checkDefaultBranch(t, defaultBranchCases[index-3], envelope.StatusCode, nav.HTML)
					} else if envelope.StatusCode != 403 || strings.Contains(nav.HTML, "Chart analytics") {
						t.Error(envelope.StatusCode, nav.HTML)
					}
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
				t.Fatal("parallel Lambda did not finish", p.logs.String())
			}
			p.stop(t)
		})
	}
}

type defaultBranchCase struct {
	header, value string
	path, other   string
	retain        bool
	status        int
	text          string
}

var defaultBranchCases = []defaultBranchCase{
	{status: 200, text: "Analytics default"},
	{header: "X-Demo-Default-Failure", value: "error", status: 500, text: "Analytics could not be loaded."},
	{header: "X-Demo-Default-Failure", value: "missing", status: 404, text: "Analytics is unavailable for this selection."},
	{header: "X-Demo-Analytics-Metadata-Deny", value: "yes", status: 403, text: "Access denied"},
	{path: "/boards/drill", status: 200, text: "Detail drill", other: "Team default"},
	{path: "/boards/drill", header: "X-Demo-Detail-Deny", value: "yes", status: 403, text: "Access denied"},
	{path: "/boards/reports", status: 200, text: "Report page", other: "Team default"},
	{path: "/boards/reports/summary", retain: true, status: 200, text: "Report page", other: "Report summary"},
	{path: "/boards/drill", retain: true, status: 200, text: "Report page", other: "Report summary"},
	{path: "/boards/activity", retain: true, header: "X-Demo-Report-Summary-Deny", value: "yes", status: 403, text: "Access denied"},
}

func checkDefaultBranch(t *testing.T, tc defaultBranchCase, status int, content string) {
	t.Helper()
	if status != tc.status || !strings.Contains(content, tc.text) || strings.Contains(content, "demonstration default loader failure") {
		t.Errorf("default branch: status=%d wanted=%d body=%s", status, tc.status, content)
	}
	other := tc.other
	if other == "" {
		other = "Team settings"
	}
	if tc.status != 403 && (!strings.Contains(content, "data-analytics-shell") || !strings.Contains(content, "Analytics shell") || !strings.Contains(content, other)) {
		t.Errorf("default branch lost wrappers/other branch: %s", content)
	}
	if tc.status == 403 && strings.Contains(content, "data-analytics-shell") {
		t.Errorf("default denial leaked retained slot: %s", content)
	}
	if tc.status == 200 {
		want := "settings"
		if strings.Contains(tc.path, "/reports") || tc.retain {
			want = "reports"
		} else if tc.path == "/boards/drill" {
			want = "index"
		}
		marker := `aria-label="Board selections children" data-selection-index="0">` + want + `</output>`
		if !strings.Contains(content, marker) || !strings.Contains(content, "data-nextgo-selection=") {
			t.Errorf("missing selected-segment SSR for %s (wanted %s)", tc.path, want)
		}
	}
}
