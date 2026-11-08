//go:build deployment && linux

package deployment

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/serverless"
)

type rieExecutable struct {
	process                                  *process
	client                                   *http.Client
	invokeURL, runtimeAddress, publicAddress string
	mode, app                                string
	sequence                                 atomic.Int64
	children                                 map[int]bool
	lastRuntime                              int
	closed                                   bool
}

var regexpRuntimeAttribute = regexp.MustCompile(`data-nextgo-runtime="([^"]+)"`)

func pinnedRIEFile(t *testing.T, name, digestName string) string {
	t.Helper()
	path, digest := os.Getenv(name), os.Getenv(digestName)
	if !filepath.IsAbs(path) || len(digest) != 64 {
		t.Fatalf("pin %s and %s", name, digestName)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 128<<20 {
		t.Fatalf("invalid pinned executable %s: %v", name, err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, 128<<20+1))
	if err != nil || n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != digest {
		t.Fatalf("%s executable hash mismatch", name)
	}
	return path
}

func newRIEExecutable(t *testing.T, mode string, scope generatedActionScope, extra []string) *rieExecutable {
	t.Helper()
	if os.Getenv("NEXTGO_RIE_BINARY") == "" {
		t.Skip("set pinned NEXTGO_RIE_BINARY for official AWS emulator qualification")
	}
	if expected := os.Getenv("NEXTGO_TEST_UID"); expected != "" && expected != strconv.Itoa(os.Getuid()) {
		t.Fatalf("expected UID %s, got %d", expected, os.Getuid())
	}
	rie := pinnedRIEFile(t, "NEXTGO_RIE_BINARY", "NEXTGO_RIE_SHA256")
	app := pinnedRIEFile(t, "NEXTGO_TEST_BINARY", "NEXTGO_TEST_BINARY_SHA256")
	publicAddress, runtimeAddress := address(t), address(t)
	if publicAddress == runtimeAddress {
		t.Fatal("RIE listener collision")
	}
	dir := t.TempDir()
	cmd := exec.Command(rie, "--log-level", "warn", "--runtime-interface-emulator-address", publicAddress, "--runtime-api-address", runtimeAddress, app, "-env-dir", dir)
	cmd.Dir = dir
	// A complete synthetic environment: the emulator never inherits cloud
	// credentials, identities, proxy configuration or application files.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + dir, "GOMAXPROCS=1", "AWS_LAMBDA_FUNCTION_TIMEOUT=8", "AWS_LAMBDA_FUNCTION_MEMORY_SIZE=256", "AWS_LAMBDA_FUNCTION_NAME=nextgo-local-conformance", "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode}
	cmd.Env = append(cmd.Env, scope.environment()...)
	cmd.Env = append(cmd.Env, "NEXTGO_DEMO_COOKIE_KEY="+sessionSigningKey)
	cmd.Env = append(cmd.Env, extra...)
	p := &process{cmd: cmd, done: make(chan struct{}), logs: &capture{}}
	cmd.Stdout, cmd.Stderr = p.logs, p.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	h := &rieExecutable{process: p, client: &http.Client{Timeout: 12 * time.Second}, invokeURL: "http://" + publicAddress + "/2015-03-31/functions/function/invocations", runtimeAddress: runtimeAddress, publicAddress: publicAddress, mode: mode, app: app, children: map[int]bool{}}
	t.Cleanup(func() { h.close(t) })
	for deadline := time.Now().Add(8 * time.Second); ; {
		connection, err := net.DialTimeout("tcp", publicAddress, 100*time.Millisecond)
		if err == nil {
			connection.Close()
			break
		}
		select {
		case <-p.done:
			t.Fatal("RIE exited before listening", p.err, p.logs.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("RIE readiness timeout", p.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return h
}

func (h *rieExecutable) observeRuntime(t *testing.T) {
	t.Helper()
	// /proc/task/*/children is absent on some kernels. Establish ownership
	// from the parent's PID using stat, then inspect only that child's exe.
	processes, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	found := map[int]bool{}
	for _, process := range processes {
		pid, err := strconv.Atoi(process.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", process.Name(), "stat"))
		if err != nil {
			continue
		}
		end := bytes.LastIndexByte(stat, ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(stat[end+1:]))
		if len(fields) < 2 || fields[1] != strconv.Itoa(h.process.cmd.Process.Pid) {
			continue
		}
		executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			t.Fatal("cannot identify owned RIE child", pid, err)
		}
		if executable == h.app {
			found[pid] = true
			h.children[pid] = true
			h.lastRuntime = pid
		}
	}
	if len(found) != 1 {
		t.Fatal("expected one owned Go runtime", found, h.process.logs.String())
	}
}

func (h *rieExecutable) raw(t *testing.T, event any) (http.Header, []byte) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.client.Post(h.invokeURL, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err, h.process.logs.String())
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 9<<20+1))
	if err != nil || len(body) > 9<<20 || response.StatusCode != 200 {
		t.Fatal("RIE invocation transport", response.StatusCode, err, h.process.logs.String())
	}
	h.observeRuntime(t)
	return response.Header.Clone(), body
}

func (h *rieExecutable) send(t *testing.T, event actionViewEvent) generatedActionReply {
	t.Helper()
	u, err := url.Parse(event.path)
	if err != nil || !strings.HasPrefix(event.path, "/") || u.Host != "" {
		t.Fatal("invalid RIE application path", event.path, err)
	}
	headers := map[string]string{"host": "cookie-session.test"}
	var cookies []string
	for name, values := range event.headers {
		if strings.EqualFold(name, "Cookie") {
			cookies = append(cookies, values...)
		} else {
			headers[strings.ToLower(name)] = strings.Join(values, ",")
		}
	}
	request := serverless.Request{Version: "2.0", RawPath: u.EscapedPath(), RawQueryString: u.RawQuery, Headers: headers, Cookies: cookies, Body: event.payload, RequestContext: serverless.RequestContext{DomainName: headers["host"], RequestID: strconv.FormatInt(h.sequence.Add(1), 10), HTTP: serverless.HTTPContext{Method: event.method, Path: u.EscapedPath(), Protocol: "HTTP/1.1", SourceIP: "127.0.0.1"}}}
	_, body := h.raw(t, request)
	var envelope serverless.Response
	var payload []byte
	if h.mode == "buffered" {
		if err = json.Unmarshal(body, &envelope); err != nil {
			t.Fatal("RIE buffered envelope", err, string(body))
		}
		payload = []byte(envelope.Body)
		if envelope.IsBase64Encoded {
			payload, err = base64.StdEncoding.DecodeString(envelope.Body)
			if err != nil {
				t.Fatal(err)
			}
		}
	} else {
		metadata, content, ok := bytes.Cut(body, make([]byte, 8))
		if !ok {
			t.Fatal("RIE streaming preamble missing", string(body))
		}
		if err = json.Unmarshal(metadata, &envelope); err != nil {
			t.Fatal(err)
		}
		payload = content
	}
	if envelope.StatusCode < 100 || envelope.StatusCode > 599 {
		t.Fatal("invalid RIE application response", string(body))
	}
	output := make(http.Header)
	for name, value := range envelope.Headers {
		if strings.EqualFold(name, "Set-Cookie") {
			t.Fatal("folded Lambda Set-Cookie metadata")
		}
		output.Set(name, value)
	}
	for _, cookie := range envelope.Cookies {
		output.Add("Set-Cookie", cookie)
	}
	return generatedActionReply{envelope.StatusCode, output, payload}
}

func (h *rieExecutable) close(t *testing.T) {
	t.Helper()
	if h.closed {
		return
	}
	h.closed = true
	h.client.CloseIdleConnections()
	select {
	case <-h.process.done:
	default:
		_ = h.process.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-h.process.done:
		case <-time.After(8 * time.Second):
			_ = h.process.cmd.Process.Kill()
			<-h.process.done
			t.Error("RIE required forced parent termination")
		}
	}
	if h.process.err != nil {
		t.Error("RIE exit", h.process.err, h.process.logs.String())
	}
	// Upstream RIE owns its runtime's shutdown and normally uses SIGKILL. This
	// checks ownership/absence, never claims graceful application shutdown.
	for pid := range h.children {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); !os.IsNotExist(err) {
			t.Error("RIE runtime process remains", pid, err)
		}
	}
	for _, addr := range []string{h.publicAddress, h.runtimeAddress} {
		connection, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			connection.Close()
			t.Error("RIE listener remains", addr)
		}
	}
	t.Logf("RIE cleanup: parent exited, %d owned Go runtime PID(s) absent, both listeners closed; upstream runtime shutdown is not graceful-drain evidence", len(h.children))
}

func TestAWSRIEExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				h := newRIEExecutable(t, mode, scope, nil)
				defer h.close(t)
				send := func(method, path string, headers http.Header) generatedActionReply {
					return h.send(t, actionViewEvent{method: method, path: path, headers: headers})
				}
				var runtimePath string
				healthPath := scope.prefix() + "/api/health"
				if scope.slash {
					healthPath += "/"
				}
				t.Run("cold-and-warm-health", func(t *testing.T) {
					for n := 0; n < 2; n++ {
						reply := send("GET", healthPath, nil)
						if reply.status != 200 || !bytes.Contains(reply.data, []byte(`"framework":"Next.go"`)) {
							t.Fatal(reply.status, string(reply.data))
						}
					}
					if len(h.children) != 1 {
						t.Fatal("runtime not reused")
					}
				})
				t.Run("native-SSR", func(t *testing.T) {
					path := scope.prefix() + "/client-only"
					if scope.slash {
						path += "/"
					}
					reply := send("GET", path, nil)
					if reply.status != 200 || !bytes.Contains(reply.data, []byte(`data-client-only-page=""`)) {
						t.Fatal(reply.status, string(reply.data))
					}
					match := regexpRuntimeAttribute.FindSubmatch(reply.data)
					if len(match) != 2 {
						t.Fatal("missing embedded runtime URL")
					}
					runtimePath = string(match[1])
				})
				t.Run("navigation-envelope", func(t *testing.T) {
					path := scope.prefix() + "/about"
					if scope.slash {
						path += "/"
					}
					reply := send("GET", path, http.Header{"X-Nextgo-Navigate": {"1"}})
					var envelope nextgo.Envelope
					if reply.status != 200 || json.Unmarshal(reply.data, &envelope) != nil || envelope.Version != nextgo.NavigationProtocolVersion || envelope.URL != path || envelope.Title != "Architecture — Next.go" {
						t.Fatal(reply.status, string(reply.data))
					}
				})
				t.Run("HEAD", func(t *testing.T) {
					reply := send("HEAD", healthPath, nil)
					if reply.status != 200 || len(reply.data) != 0 {
						t.Fatal(reply.status, string(reply.data))
					}
				})
				t.Run("compressed-runtime-binary", func(t *testing.T) {
					if runtimePath == "" {
						t.Fatal("SSR runtime unavailable")
					}
					reply := send("GET", runtimePath+"app.wasm", http.Header{"Accept-Encoding": {"gzip"}})
					if reply.status != 200 || reply.headers.Get("Content-Encoding") != "gzip" {
						t.Fatal(reply.status, reply.headers)
					}
					reader, err := gzip.NewReader(bytes.NewReader(reply.data))
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					header := make([]byte, 8)
					if _, err = io.ReadFull(reader, header); err != nil || !bytes.Equal(header, []byte{0, 'a', 's', 'm', 1, 0, 0, 0}) {
						t.Fatal("runtime bytes", header, err)
					}
				})
				t.Run("warm-generated-mutation", func(t *testing.T) {
					initial := send("GET", scope.page(), nil)
					snapshot, err := generatedActionSnapshot(initial.data, scope)
					if err != nil || snapshot.count != 0 {
						t.Fatal(snapshot, err)
					}
					reply := h.send(t, actionViewEvent{method: "POST", path: scope.endpoint(), payload: `{"amount":2}`, headers: http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}, "Origin": {"https://cookie-session.test"}, "X-Nextgo-Action-Protocol": {"1"}, "X-Nextgo-Action-Revision": {"v1"}}})
					if err := generatedActionResult(reply, 2, 1, scope.page()); err != nil {
						t.Fatal(err)
					}
					after := send("GET", scope.page(), nil)
					snapshot, err = generatedActionSnapshot(after.data, scope)
					if err != nil || snapshot.count != 2 || snapshot.revision != 1 {
						t.Fatal(snapshot, err)
					}
				})
				t.Run("unsupported-event-error-and-recovery", func(t *testing.T) {
					_, body := h.raw(t, map[string]string{"version": "1.0"})
					var failure struct {
						Type string `json:"errorType"`
					}
					if json.Unmarshal(body, &failure) != nil || failure.Type != "NextGoInvocationError" {
						t.Fatal("missing invocation error", string(body))
					}
					reply := send("GET", healthPath, nil)
					if reply.status != 200 {
						t.Fatal("runtime did not recover", reply.status)
					}
				})
			})
		}
	}
}

func TestAWSRIECookieSessionExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				exerciseCookieSession(t, func(scope generatedActionScope, extra []string) *sessionExecutable {
					h := newRIEExecutable(t, mode, scope, extra)
					return &sessionExecutable{origin: "https://cookie-session.test", secure: true, close: func() { h.close(t) }, send: func(event actionViewEvent) generatedActionReply { return h.send(t, event) }}
				}, scope)
			})
		}
	}
}
