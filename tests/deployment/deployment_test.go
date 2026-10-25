//go:build deployment

// These black-box checks exercise the generated executable, not an in-process
// substitute. Run scripts/test-deployment-container.sh for an isolated Linux run.
package deployment

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type capture struct {
	mu   sync.Mutex
	text bytes.Buffer
}

func (b *capture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}
func (b *capture) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.text.String() }

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	logs *capture
}

func start(t *testing.T, env []string, args ...string) *process {
	t.Helper()
	if expected := os.Getenv("NEXTGO_TEST_UID"); expected != "" && expected != strconv.Itoa(os.Getuid()) {
		t.Fatalf("expected UID %s, got %d", expected, os.Getuid())
	}
	binary := os.Getenv("NEXTGO_TEST_BINARY")
	if binary == "" {
		t.Skip("set NEXTGO_TEST_BINARY to a built demo server")
	}
	cmd := exec.Command(binary, args...)
	// AWS environment on the caller must not accidentally select the runtime.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "AWS_LAMBDA_") && !strings.HasPrefix(key, "NEXTGO_LAMBDA_") && !strings.HasPrefix(key, "NEXTGO_DEMO_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	cmd.Dir = "/" // no checkout, application files, writable cwd or relative assets
	p := &process{cmd: cmd, done: make(chan struct{}), logs: &capture{}}
	cmd.Stdout = p.logs
	cmd.Stderr = p.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
			return
		default:
		}
		_ = p.cmd.Process.Kill()
		<-p.done
	})
	return p
}
func (p *process) stop(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatalf("SIGTERM: %v\n%s", p.err, p.logs.String())
		}
	case <-time.After(7 * time.Second):
		t.Fatal("SIGTERM did not drain")
	}
}
func address(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	return addr
}

var versionRE = regexp.MustCompile(`/_nextgo/([a-f0-9]+)/`)
var configurationRE = regexp.MustCompile(`data-nextgo-configuration="([a-f0-9]{64})"`)

func body(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if response.Header.Get("Content-Encoding") == "gzip" {
		decoder, err := gzip.NewReader(reader)
		if err != nil {
			t.Fatal(err)
		}
		defer decoder.Close()
		reader = decoder
	}
	data, err := io.ReadAll(io.LimitReader(reader, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestSelfHostedExecutable(t *testing.T) {
	for _, base := range []string{"", "/demo"} {
		t.Run("base="+base, func(t *testing.T) { selfHostedExecutable(t, base, false) })
	}
}
func TestSelfHostedSlashExecutable(t *testing.T) { selfHostedExecutable(t, "/slash", true) }
func TestLambdaSlashExecutable(t *testing.T)     { lambdaExecutable(t, "/slash", true) }
func mountedURL(base, path string) string {
	if path == "/" && base != "" {
		return base
	}
	return base + path
}
func selfHostedExecutable(t *testing.T, base string, slash bool) {
	addr := address(t)
	assetPrefix := ""
	if base != "" {
		assetPrefix = "/cdn"
	}
	p := start(t, []string{"NEXTGO_DEMO_BASE_PATH=" + base, "NEXTGO_DEMO_ASSET_PREFIX=" + assetPrefix, "NEXTGO_DEMO_TRAILING_SLASH=" + strconv.FormatBool(slash), "NEXTGO_DEMO_READER_TOKEN=" + readerToken, "NEXTGO_DEMO_ADMIN_TOKEN=" + adminToken}, "-addr", addr)
	origin := "http://" + addr
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableCompression: true}}
	defer client.CloseIdleConnections()
	ready := false
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		response, err := client.Get(origin + mountedURL(base, "/api/health"))
		if err == nil {
			data := body(t, response)
			if response.StatusCode == 200 && bytes.Contains(data, []byte(`"ok":true`)) {
				ready = true
				break
			}
		}
		select {
		case <-p.done:
			t.Fatalf("startup: %v\n%s", p.err, p.logs.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if !ready {
		t.Fatalf("not ready\n%s", p.logs.String())
	}
	response, err := client.Get(origin + mountedURL(base, "/"))
	if err != nil {
		t.Fatal(err)
	}
	html := body(t, response)
	if response.StatusCode != 200 || !bytes.Contains(html, []byte("First counter")) || !bytes.Contains(html, []byte("Go, all the way")) {
		t.Fatal("missing useful SSR")
	}
	configuration := configurationRE.FindSubmatch(html)
	if len(configuration) != 2 || response.Header.Get("X-Nextgo-Configuration") != string(configuration[1]) {
		t.Fatal("document configuration identity")
	}
	optimisticHTTP(t, client, origin, base, slash)
	version := versionRE.FindSubmatch(html)
	if len(version) != 2 {
		t.Fatal("missing versioned runtime")
	}
	for _, path := range []string{"/posts/html-first", "/lifecycle", "/api/health", "/assets/style.css"} {
		response, err := client.Get(origin + mountedURL(base, path))
		if err != nil {
			t.Fatal(err)
		}
		data := body(t, response)
		if response.StatusCode != 200 || len(data) == 0 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	response, err = client.Get(origin + mountedURL(base, "/missing"))
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 404 {
		t.Fatal("missing status")
	}
	req, _ := http.NewRequest("GET", origin+mountedURL(base, "/posts/html-first"), nil)
	req.Header.Set("X-Nextgo-Navigate", "1")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version, Status            int
		HTML, Title, Configuration string
		Document                   struct {
			Language  string `json:"lang"`
			Direction string `json:"dir"`
			Viewport  string `json:"viewport"`
		}
	}
	if err := json.Unmarshal(body(t, response), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Title != "Start with the HTML — Next.go" || envelope.Configuration != string(configuration[1]) || response.Header.Get("X-Nextgo-Configuration") != envelope.Configuration || envelope.Version != 3 || envelope.Status != 200 || !strings.Contains(envelope.HTML, "Start with the HTML") || response.Header.Get("X-Nextgo-Protocol") != "3" {
		t.Fatal("invalid navigation response")
	}
	if envelope.Document.Language != "en" || envelope.Document.Direction != "ltr" || envelope.Document.Viewport != "width=device-width,initial-scale=1" {
		t.Fatal("missing navigation document attributes")
	}
	localeURL := origin + mountedURL(base, "/locale")
	if slash {
		localeURL += "/"
	}
	localeURL += "?lang=ar"
	response, err = client.Get(localeURL)
	if err != nil {
		t.Fatal(err)
	}
	localeHTML := body(t, response)
	if response.StatusCode != 200 || !bytes.Contains(localeHTML, []byte(`<html lang="ar" dir="rtl">`)) || !bytes.Contains(localeHTML, []byte("<title>مرحبا — Next.go</title>")) || !bytes.Contains(localeHTML, []byte("viewport-fit=cover")) || !bytes.Contains(localeHTML, []byte(`media="(prefers-color-scheme: dark)"`)) {
		t.Fatal("missing server document metadata")
	}
	req, _ = http.NewRequest("GET", localeURL, nil)
	req.Header.Set("X-Nextgo-Navigate", "1")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body(t, response), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || envelope.Title != "مرحبا — Next.go" || envelope.Document.Language != "ar" || envelope.Document.Direction != "rtl" || !strings.Contains(envelope.Document.Viewport, "viewport-fit=cover") || envelope.Configuration != string(configuration[1]) {
		t.Fatal("invalid translated navigation document")
	}
	checkLifetimeExecutable(t, client, origin, base, slash)
	checkMarkedProfileExecutable(t, client, origin, base, slash, "/profiles", "Profiled", "cache-publish-stale", 1, 2000)
	checkMarkedProfileExecutable(t, client, origin, base, slash, "/evergreen", "Evergreen", "evergreen-publish", 2, 300000)
	req, _ = http.NewRequest("GET", origin+assetPrefix+"/_nextgo/"+string(version[1])+"/app.wasm", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(response.Header.Get("Cache-Control"), "immutable") || !strings.Contains(response.Header.Get("Content-Type"), "application/wasm") {
		t.Fatal("runtime asset contract")
	}
	coreData := body(t, response)
	if !bytes.HasPrefix(coreData, []byte{0, 'a', 's', 'm'}) {
		t.Fatal("invalid WASM")
	}
	moduleFiles := lazyModuleFiles(coreData)
	if len(moduleFiles) == 0 {
		t.Fatal("core has no lazy module descriptor")
	}
	t.Logf("checking %d unique lazy WASM assets", len(moduleFiles))
	if bytes.Contains(coreData, []byte(lazyCounterProof)) {
		t.Fatal("lazy counter client implementation leaked into core")
	}
	legacyProof := false
	for _, moduleFile := range moduleFiles {
		response, err = client.Get(origin + assetPrefix + "/_nextgo/" + string(version[1]) + "/" + moduleFile)
		if err != nil {
			t.Fatal(err)
		}
		moduleData := body(t, response)
		if err := checkLazyModule(moduleFile, moduleData); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
			t.Fatal("module asset is not immutable", moduleFile, response.StatusCode, response.Header)
		}
		legacyProof = legacyProof || bytes.Contains(moduleData, []byte(lazyCounterProof))
	}
	if !legacyProof {
		t.Fatal("lazy counter client implementation absent from all lazy modules")
	}

	for _, token := range []string{"", readerToken, adminToken} {
		for _, path := range []string{"/restricted", "/api/report", "/_nextgo/actions/review-report"} {
			method := "GET"
			var payload io.Reader
			if strings.HasPrefix(path, "/_nextgo/actions/") {
				method = "POST"
				payload = strings.NewReader("{}")
			}
			req, _ := http.NewRequest(method, origin+mountedURL(base, path), payload)
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data := body(t, response)
			want := accessExpectedStatus(token)
			if response.StatusCode != want || response.Header.Get("Cache-Control") != accessExpectedCache(path, want) {
				t.Fatalf("access %s: %d %s", path, response.StatusCode, data)
			}
			if want == 401 && response.Header.Get("WWW-Authenticate") != `Bearer realm="nextgo-demo"` {
				t.Fatal("missing challenge")
			}
			if token != "" && bytes.Contains(data, []byte(token)) {
				t.Fatal("credential reflected")
			}
			if path != "/_nextgo/actions/review-report" && bytes.Contains(data, []byte("Only an authorized server request")) != (want == 200) {
				t.Fatal("protected data response", path, want)
			}
		}
	}
	p.stop(t)
	if !strings.Contains(p.logs.String(), "demo completion callback: rendered /lifecycle") {
		t.Fatal("completion callback was not drained")
	}
}

func checkMarkedProfileExecutable(t *testing.T, client *http.Client, origin, base string, slash bool, route, title, action string, before int, maxWindow int64) {
	t.Helper()
	profileURL := origin + mountedURL(base, route)
	if slash {
		profileURL += "/"
	}
	response, err := client.Get(profileURL)
	if err != nil {
		t.Fatal(err)
	}
	profileHTML := body(t, response)
	window, windowErr := strconv.ParseInt(response.Header.Get("X-Nextgo-Stale-Millis"), 10, 64)
	if response.StatusCode != 200 || windowErr != nil || window <= 0 || window > maxWindow || response.Header.Get("Cache-Control") != "public, max-age=0, must-revalidate" || !bytes.Contains(profileHTML, []byte("<title>"+title+" snapshot ")) {
		t.Fatal("profile executable response", response.StatusCode, response.Header)
	}
	// Commit a write and mark this still-fresh snapshot stale. Publication of
	// the old view must precede the background refresh in the actual binary.
	req, _ := http.NewRequest("POST", origin+mountedURL(base, "/_nextgo/actions/"+action), strings.NewReader("{}"))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal("mark action", response.StatusCode)
	}
	_ = body(t, response)
	response, err = client.Get(profileURL)
	if err != nil {
		t.Fatal(err)
	}
	if data := body(t, response); !bytes.Contains(data, []byte(fmt.Sprintf("<title>%s snapshot %d — Next.go</title>", title, before))) {
		t.Fatal("marked stale view not published")
	}
	for until := time.Now().Add(2 * time.Second); ; {
		response, err = client.Get(profileURL)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body(t, response), []byte(fmt.Sprintf("<title>%s snapshot %d — Next.go</title>", title, before+1))) {
			break
		}
		if !time.Now().Before(until) {
			t.Fatal("marked native refresh did not publish")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func checkLifetimeExecutable(t *testing.T, client *http.Client, origin, base string, slash bool) {
	t.Helper()
	pageURL := origin + mountedURL(base, "/cache")
	if slash {
		pageURL += "/"
	}
	read := func() string {
		t.Helper()
		response, err := client.Get(pageURL)
		if err != nil {
			t.Fatal(err)
		}
		data := body(t, response)
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Nextgo-Prefetch") != "" {
			t.Fatal("lifetime response cache contract", response.StatusCode, response.Header)
		}
		return string(data)
	}
	if !strings.Contains(read(), "<title>Cached snapshot 0 — Next.go</title>") {
		t.Fatal("initial cached snapshot")
	}
	req, _ := http.NewRequest("POST", origin+mountedURL(base, "/_nextgo/actions/cache-source-increment"), strings.NewReader("{}"))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 200 {
		t.Fatal("source action", response.StatusCode)
	}
	time.Sleep(1200 * time.Millisecond) // Cross the explicit one-second fresh window.
	if !strings.Contains(read(), "<title>Cached snapshot 0 — Next.go</title>") {
		t.Fatal("stale read blocked or discarded last good snapshot")
	}
	for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
		if strings.Contains(read(), "<title>Cached snapshot 1 — Next.go</title>") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("native background refresh did not publish")
}

type httpEnvelope struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Base64     bool              `json:"isBase64Encoded"`
}
type invocation struct{ path, method, token string }

var readerToken = "nextgo-test-reader-" + strings.Repeat("r", 32)
var adminToken = "nextgo-test-admin-" + strings.Repeat("a", 32)

func accessExpectedCache(path string, status int) string {
	if strings.HasPrefix(path, "/_nextgo/actions/") && status == 200 {
		return "no-store"
	}
	return "private, no-store"
}
func accessExpectedStatus(token string) int {
	if token == adminToken {
		return 200
	}
	if token == readerToken {
		return 403
	}
	return 401
}

func TestLambdaExecutable(t *testing.T) {
	for _, base := range []string{"", "/demo"} {
		t.Run("base="+base, func(t *testing.T) { lambdaExecutable(t, base, false) })
	}
}
func lambdaExecutable(t *testing.T, base string, slash bool, localization ...string) {
	style := ""
	if len(localization) > 0 {
		style = localization[0]
	}
	lambdaExecutableSettings(t, base, slash, style, false)
}
func lambdaExecutableSettings(t *testing.T, base string, slash bool, localization string, tracing bool) {
	lambdaExecutableRuntimePolicy(t, base, slash, localization, tracing, false)
}
func lambdaExecutableRuntimePolicy(t *testing.T, base string, slash bool, localization string, tracing, transitions bool) {
	localized := localization != ""
	domainMode := localization == "domain"
	localePrefix := ""
	hostname := "demo.test"
	localeDomains := ""
	if localized {
		localePrefix = "/fr"
	}
	if domainMode {
		localePrefix = ""
		hostname = "fr.demo.test"
		localeDomains = "demo.test=en,fr.demo.test=fr"
	}

	assetPrefix := ""
	if base != "" {
		assetPrefix = "/cdn"
	}
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			requests := []invocation{{"/", "GET", ""}, {"/posts/html-first", "GET", ""}, {"/locale", "GET", ""}, {"/profiles", "GET", ""}, {"/api/health", "GET", ""}, {"/api/health", "HEAD", ""}, {"/missing", "GET", ""}, {"/stream", "GET", ""}, {"/lifecycle", "GET", ""}}
			for _, token := range []string{"", readerToken, adminToken} {
				requests = append(requests, invocation{"/restricted", "GET", token}, invocation{"/api/report", "GET", token}, invocation{"/_nextgo/actions/review-report", "POST", token})
			}
			cacheStart := len(requests)
			requests = append(requests, invocation{"/cache", "GET", ""}, invocation{"/_nextgo/actions/cache-source-increment", "POST", ""}, invocation{"/cache", "GET", ""}, invocation{"/cache", "GET", ""})
			markStart := len(requests)
			requests = append(requests, invocation{"/_nextgo/actions/increment", "POST", ""}, invocation{"/profiles", "GET", ""}, invocation{"/_nextgo/actions/cache-publish-stale", "POST", ""}, invocation{"/profiles", "GET", ""}, invocation{"/profiles", "GET", ""})
			evergreenStart := len(requests)
			requests = append(requests, invocation{"/evergreen", "GET", ""}, invocation{"/_nextgo/actions/evergreen-publish", "POST", ""}, invocation{"/evergreen", "GET", ""}, invocation{"/evergreen", "GET", ""})
			if localized {
				requests = append(requests, invocation{"/translations", "GET", ""}, invocation{"/api/locale", "GET", ""})
			}
			optimisticStart := len(requests)
			requests = append(requests, invocation{"/optimistic", "GET", ""}, invocation{"/_nextgo/actions/optimistic-increment/v1", "POST", ""}, invocation{"/_nextgo/actions/optimistic-increment/v1", "POST", ""}, invocation{"/optimistic", "GET", ""}, invocation{"/_nextgo/actions/optimistic-increment/v1", "POST", ""}, invocation{"/optimistic", "GET", ""})
			requests = append(requests, invocation{"/lazy", "GET", ""})
			var mu sync.Mutex
			legacyProof := false
			var calls atomic.Int32
			var reports atomic.Int32
			all := make(chan struct{})
			failures := make(chan error, 16)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(calls.Add(1)) - 1
					mu.Lock()
					if index >= len(requests) {
						mu.Unlock()
						<-r.Context().Done()
						return
					}
					item := requests[index]
					mu.Unlock()
					if index == cacheStart+2 {
						time.Sleep(1200 * time.Millisecond)
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(15*time.Second).UnixMilli(), 10))
					publicPath := mountedURL(base, item.path)
					if localized && (!strings.HasPrefix(item.path, "/api/") && !strings.HasPrefix(item.path, "/_nextgo/") || item.path == "/api/locale") {
						publicPath = mountedURL(base+localePrefix, item.path)
					}

					if slash && !strings.HasPrefix(item.path, "/_nextgo/") && !strings.HasSuffix(publicPath, "/") {
						publicPath += "/"
					}
					if strings.HasSuffix(item.path, ".wasm") {
						publicPath = assetPrefix + item.path
					}
					headers := map[string]string{"host": hostname, "accept-encoding": "gzip"}
					requestBody := ""
					if item.token != "" {
						headers["authorization"] = "Bearer " + item.token
					}
					if item.method == "POST" {
						requestBody = "{}"
						headers["origin"] = "https://" + hostname
						headers["content-type"] = "application/json"
						if index == optimisticStart+1 || index == optimisticStart+2 {
							headers["accept"] = "application/json"
							requestBody = `{"amount":5}`
							if index == optimisticStart+2 {
								requestBody = `{"amount":0}`
							}
						}
						if index == optimisticStart+4 {
							headers["content-type"] = "application/x-www-form-urlencoded"
							headers["accept"] = "text/html"
							requestBody = "amount=1"
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": publicPath, "rawQueryString": "", "body": requestBody, "headers": headers, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": item.method, "path": item.path, "protocol": "HTTP/1.1", "sourceIp": "127.0.0.1"}}})
					return
				}
				fail := func(err error) { failures <- err; w.WriteHeader(500) }
				parts := strings.Split(r.URL.Path, "/")
				if len(parts) < 3 || parts[len(parts)-1] != "response" {
					fail(fmt.Errorf("unexpected report %s", r.URL.Path))
					return
				}
				id, err := strconv.Atoi(parts[len(parts)-2])
				if err != nil {
					fail(err)
					return
				}
				mu.Lock()
				if id < 0 || id >= len(requests) {
					mu.Unlock()
					fail(fmt.Errorf("invalid id"))
					return
				}
				item := requests[id]
				mu.Unlock()
				var envelope httpEnvelope
				var payload []byte
				if mode == "buffered" {
					if err := json.NewDecoder(io.LimitReader(r.Body, 6<<20)).Decode(&envelope); err != nil {
						fail(err)
						return
					}
					payload = []byte(envelope.Body)
					if envelope.Base64 {
						payload, err = base64.StdEncoding.DecodeString(envelope.Body)
						if err != nil {
							fail(err)
							return
						}
					}
					if envelope.Headers["content-encoding"] == "gzip" {
						decoder, err := gzip.NewReader(bytes.NewReader(payload))
						if err != nil {
							fail(err)
							return
						}
						payload, err = io.ReadAll(decoder)
						decoder.Close()
						if err != nil {
							fail(err)
							return
						}
					}
				} else {
					if r.Header.Get("Lambda-Runtime-Function-Response-Mode") != "streaming" || len(r.TransferEncoding) != 1 || r.TransferEncoding[0] != "chunked" {
						fail(fmt.Errorf("invalid streaming transport"))
						return
					}
					reader := bufio.NewReader(io.LimitReader(r.Body, 16<<20))
					var prelude []byte
					for len(prelude) < 16<<10 {
						value, err := reader.ReadByte()
						if err != nil {
							fail(err)
							return
						}
						if value == 0 {
							break
						}
						prelude = append(prelude, value)
					}
					var delimiter [7]byte
					if _, err := io.ReadFull(reader, delimiter[:]); err != nil || delimiter != [7]byte{} {
						fail(fmt.Errorf("invalid stream delimiter: %v", err))
						return
					}
					if err := json.Unmarshal(prelude, &envelope); err != nil {
						fail(err)
						return
					}
					var decoded io.Reader = reader
					if envelope.Headers["content-encoding"] == "gzip" {
						decoder, err := gzip.NewReader(reader)
						if err != nil {
							fail(err)
							return
						}
						defer decoder.Close()
						decoded = decoder
					}
					var shell time.Time
					scratch := make([]byte, 4096)
					for {
						n, err := decoded.Read(scratch)
						payload = append(payload, scratch[:n]...)
						if item.path == "/stream" && shell.IsZero() && bytes.Contains(payload, []byte("data-stream-loading")) {
							shell = time.Now()
							if bytes.Contains(payload, []byte("data-nextgo-chunk=")) {
								fail(fmt.Errorf("stream did not expose early shell"))
								return
							}
						}
						if err == io.EOF {
							break
						}
						if err != nil {
							fail(err)
							return
						}
					}
					if item.path == "/stream" && (shell.IsZero() || time.Since(shell) < 200*time.Millisecond) {
						fail(fmt.Errorf("stream was buffered"))
						return
					}
					if r.Trailer.Get("Lambda-Runtime-Function-Error-Type") != "" {
						fail(fmt.Errorf("stream error trailers: %v", r.Trailer))
						return
					}
				}
				wantStatus := 200
				if item.path == "/missing" {
					wantStatus = 404
				}
				if item.path == "/restricted" || item.path == "/api/report" || item.path == "/_nextgo/actions/review-report" {
					wantStatus = accessExpectedStatus(item.token)
				}
				if id == optimisticStart+2 {
					wantStatus = 422
				}
				if id == optimisticStart+4 {
					wantStatus = 303
				}
				if envelope.StatusCode != wantStatus {
					fail(fmt.Errorf("%s status %d", item.path, envelope.StatusCode))
					return
				}
				if !strings.HasPrefix(item.path, "/api/") && !strings.HasPrefix(item.path, "/_nextgo/") {
					if bytes.Contains(payload, []byte(`data-nextgo-transitions="true"`)) != transitions {
						fail(fmt.Errorf("%s transition policy", item.path))
						return
					}
					identity := configurationRE.FindSubmatch(payload)
					if len(identity) != 2 || envelope.Headers["x-nextgo-configuration"] != string(identity[1]) {
						fail(fmt.Errorf("%s configuration identity", item.path))
						return
					}
				}
				if item.path == "/restricted" || item.path == "/api/report" || item.path == "/_nextgo/actions/review-report" {
					if envelope.Headers["cache-control"] != accessExpectedCache(item.path, wantStatus) || wantStatus == 401 && envelope.Headers["www-authenticate"] != `Bearer realm="nextgo-demo"` {
						fail(fmt.Errorf("access response headers %s", item.path))
						return
					}
					if item.path == "/restricted" || item.path == "/api/report" {
						if bytes.Contains(payload, []byte("Only an authorized server request")) != (wantStatus == 200) {
							fail(fmt.Errorf("protected data response %s", item.path))
							return
						}
					}
					if item.token != "" && bytes.Contains(payload, []byte(item.token)) {
						fail(fmt.Errorf("credential reflected"))
						return
					}
				}
				switch {
				case id >= optimisticStart && id < optimisticStart+6:
					if item.method == "POST" && (envelope.Headers["x-nextgo-action-protocol"] != "1" || envelope.Headers["x-nextgo-action-revision"] != "v1") {
						fail(fmt.Errorf("missing typed mutation transport metadata"))
						return
					}
					if err := checkOptimisticResult(id-optimisticStart, envelope.StatusCode, payload); err != nil {
						fail(err)
						return
					}
				case item.method == "HEAD":
					if len(payload) != 0 {
						fail(fmt.Errorf("HEAD body"))
						return
					}
				case item.path == "/":
					version := versionRE.FindSubmatch(payload)
					if len(version) != 2 || !bytes.Contains(payload, []byte("First counter")) {
						fail(fmt.Errorf("missing SSR/runtime"))
						return
					}
					mu.Lock()
					requests = append(requests, invocation{"/_nextgo/" + string(version[1]) + "/app.wasm", "GET", ""})
					mu.Unlock()
				case strings.HasSuffix(item.path, ".wasm"):
					if strings.HasSuffix(item.path, "/app.wasm") {
						modules := lazyModuleFiles(payload)
						if len(modules) == 0 {
							fail(fmt.Errorf("core has no lazy descriptor"))
							return
						}
						t.Logf("checking %d unique Lambda lazy WASM assets", len(modules))
						if bytes.Contains(payload, []byte(lazyCounterProof)) {
							fail(fmt.Errorf("lazy counter client implementation leaked into core"))
							return
						}
						runtimeBase := strings.TrimSuffix(item.path, "app.wasm")
						mu.Lock()
						for _, module := range modules {
							requests = append(requests, invocation{runtimeBase + module, "GET", ""})
						}
						mu.Unlock()
					} else if err := checkLazyModule(item.path, payload); err != nil {
						fail(err)
						return
					} else if bytes.Contains(payload, []byte(lazyCounterProof)) {
						mu.Lock()
						legacyProof = true
						mu.Unlock()
					}

					if !bytes.HasPrefix(payload, []byte{0, 'a', 's', 'm'}) || envelope.Headers["content-encoding"] != "gzip" || !strings.Contains(envelope.Headers["cache-control"], "immutable") {
						fail(fmt.Errorf("invalid WASM transport"))
						return
					}
				case item.path == "/stream":
					if !bytes.Contains(payload, []byte("Deferred content is ready")) {
						fail(fmt.Errorf("missing deferred content"))
						return
					}
				case item.path == "/api/health":
					if !bytes.Contains(payload, []byte(`"ok":true`)) {
						fail(fmt.Errorf("invalid API"))
						return
					}
				case item.path == "/lifecycle":
					if !bytes.Contains(payload, []byte("Request completion work")) {
						fail(fmt.Errorf("missing lifecycle page"))
						return
					}
				case item.path == "/posts/html-first":
					canonical := mountedURL(base+localePrefix, item.path)
					if slash {
						canonical += "/"
					}
					if !bytes.Contains(payload, []byte("<title>Start with the HTML — Next.go</title>")) || !bytes.Contains(payload, []byte("Next.go field notes")) || !bytes.Contains(payload, []byte("https://example.com"+canonical)) {
						fail(fmt.Errorf("missing inherited dynamic metadata"))
						return
					}
					if !bytes.Contains(payload, []byte("Start with the HTML")) {
						fail(fmt.Errorf("missing dynamic page"))
						return
					}
				case item.path == "/translations":
					if !bytes.Contains(payload, []byte("Bonjour")) || !bytes.Contains(payload, []byte(`<html lang="fr" dir="ltr">`)) || envelope.Headers["content-language"] != "fr" {
						fail(fmt.Errorf("localized executable page: %s", payload))
						return
					}
				case item.path == "/api/locale":
					if !bytes.Contains(payload, []byte(`"locale":"fr"`)) {
						fail(fmt.Errorf("localized API context: %s", payload))
						return
					}
				case item.path == "/locale":
					if !bytes.Contains(payload, []byte(`<html lang="en" dir="ltr">`)) || !bytes.Contains(payload, []byte("<title>Hello — Next.go</title>")) || !bytes.Contains(payload, []byte("viewport-fit=cover")) || !bytes.Contains(payload, []byte(`name="theme-color"`)) {
						fail(fmt.Errorf("missing document metadata"))
						return
					}
				case item.path == "/cache":
					value := 0
					if id == cacheStart+3 {
						value = 1
					}
					if !bytes.Contains(payload, []byte(fmt.Sprintf("<title>Cached snapshot %d — Next.go</title>", value))) || envelope.Headers["cache-control"] != "no-store" || envelope.Headers["x-nextgo-prefetch"] != "" {
						fail(fmt.Errorf("cache lifecycle snapshot %d: %s", value, payload))
						return
					}
				case item.path == "/evergreen":
					value := 3
					if id == evergreenStart+3 {
						value = 4
					}
					window, err := strconv.ParseInt(envelope.Headers["x-nextgo-stale-millis"], 10, 64)
					if err != nil || window <= 0 || window > 300000 || envelope.Headers["cache-control"] != "public, max-age=0, must-revalidate" || !bytes.Contains(payload, []byte(fmt.Sprintf("<title>Evergreen snapshot %d — Next.go</title>", value))) {
						fail(fmt.Errorf("non-expiring snapshot %d: %s", value, payload))
						return
					}
				case item.path == "/profiles":
					if id >= markStart {
						value := 2
						if id == markStart+4 {
							value = 3
						}
						if !bytes.Contains(payload, []byte(fmt.Sprintf("<title>Profiled snapshot %d — Next.go</title>", value))) {
							fail(fmt.Errorf("marked Lambda snapshot %d: %s", value, payload))
							return
						}
					}
					window, err := strconv.ParseInt(envelope.Headers["x-nextgo-stale-millis"], 10, 64)
					if err != nil || window <= 0 || window > 2000 || envelope.Headers["cache-control"] != "public, max-age=0, must-revalidate" || !bytes.Contains(payload, []byte("<title>Profiled snapshot ")) {
						fail(fmt.Errorf("profile lifetime headers %v", envelope.Headers))
						return
					}
				}
				w.WriteHeader(202)
				mu.Lock()
				expectedReports := int32(len(requests))
				mu.Unlock()
				if reports.Add(1) == expectedReports {
					close(all)
				}
			}))
			t.Cleanup(runtime.Close)
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode, "NEXTGO_DEMO_BASE_PATH=" + base, "NEXTGO_DEMO_ASSET_PREFIX=" + assetPrefix, "NEXTGO_DEMO_TRAILING_SLASH=" + strconv.FormatBool(slash), "NEXTGO_DEMO_READER_TOKEN=" + readerToken, "NEXTGO_DEMO_ADMIN_TOKEN=" + adminToken, "NEXTGO_DEMO_LOCALES=" + strconv.FormatBool(localized), "NEXTGO_DEMO_LOCALE_DOMAINS=" + localeDomains, "NEXTGO_DEMO_TRACING=" + strconv.FormatBool(tracing), "NEXTGO_DEMO_VIEW_TRANSITIONS=" + strconv.FormatBool(transitions)})
			select {
			case <-all:
			case err := <-failures:
				t.Fatal(err)
			case <-p.done:
				t.Fatalf("runtime exited: %v\n%s", p.err, p.logs.String())
			case <-time.After(20 * time.Second):
				t.Fatal("runtime timed out")
			}
			p.stop(t)
			mu.Lock()
			foundLegacyProof := legacyProof
			mu.Unlock()
			if !foundLegacyProof {
				t.Fatal("lazy counter client implementation absent from all lazy modules")
			}
			if !strings.Contains(p.logs.String(), "demo completion callback: rendered /lifecycle") {
				t.Fatal("completion work not drained")
			}
			if tracing {
				assertExecutableTraceRecords(t, p.logs.String(), true)
			}
		})
	}
}

var lazyModuleRE = regexp.MustCompile(`module-[a-f0-9]{64}\.wasm`)

const lazyCounterProof = "lazy-client-only-proof-7d91"

func lazyModuleFiles(core []byte) []string {
	seen := make(map[string]bool)
	var files []string
	for _, match := range lazyModuleRE.FindAll(core, -1) {
		name := string(match)
		if !seen[name] {
			seen[name] = true
			files = append(files, name)
		}
	}
	return files
}

func checkLazyModule(name string, data []byte) error {
	hash := sha256.Sum256(data)
	if !strings.HasSuffix(name, fmt.Sprintf("module-%x.wasm", hash)) || !bytes.HasPrefix(data, []byte{0, 'a', 's', 'm'}) {
		return fmt.Errorf("invalid embedded lazy module %s", name)
	}
	return nil
}
