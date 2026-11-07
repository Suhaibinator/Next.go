//go:build deployment && invocation_context

package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/serverless"
)

// This focused executable test fails closed without its OWN actual prebuilt
// artifact. The external producer builds the tiny file-routed app once per SDK;
// this test never compiles, substitutes a handler, or uses the normal demo binary.
type invocationIdentityObservation struct {
	Sequence           int    `json:"sequence"`
	Invocation         string `json:"invocation"`
	Gateway            string `json:"gateway"`
	RetainedInvocation string `json:"retainedInvocation"`
	RetainedGateway    string `json:"retainedGateway"`
}
type invocationIdentityPublication struct {
	index int
	value invocationIdentityObservation
}
type invocationIdentityLog struct {
	mu   sync.Mutex
	data []byte
}

func (b *invocationIdentityLog) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(data)
	if left := (64 << 10) - len(b.data); left > 0 {
		if len(data) > left {
			data = data[:left]
		}
		b.data = append(b.data, data...)
	}
	return original, nil
}
func (b *invocationIdentityLog) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

type invocationIdentityChild struct {
	cmd   *exec.Cmd
	done  chan struct{}
	err   error
	logs  *invocationIdentityLog
	start string
}
type invocationIdentityController struct {
	mode            string
	server          *http.Server
	origin          string
	serveDone       chan error
	closing         chan struct{}
	closeOnce       sync.Once
	child           *invocationIdentityChild
	active          sync.WaitGroup
	mu              sync.Mutex
	polls           int
	accepted        [3]bool
	callbackReplied [3]bool
	accept          [3]chan struct{}
	finishAfter     [3]chan struct{}
	afterReplied    [3]chan struct{}
	publication     chan invocationIdentityPublication
	after           chan invocationIdentityPublication
	fourth          chan struct{}
	faults          chan error
}

func (c *invocationIdentityController) fault(err error) {
	select {
	case c.faults <- err:
	default:
	}
}
func invocationIdentityRead(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return nil, fmt.Errorf("bounded fixture body: %v", err)
	}
	return data, nil
}
func invocationIdentityDecode(data []byte) (invocationIdentityObservation, error) {
	var value invocationIdentityObservation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return value, errors.New("extra identity JSON")
	}
	if value.Sequence < 1 || value.Sequence > 3 {
		return value, errors.New("invalid identity sequence")
	}
	return value, nil
}
func invocationIdentityPayload(r *http.Request, mode string) (invocationIdentityObservation, error) {
	data, err := invocationIdentityRead(r.Body)
	if err != nil {
		return invocationIdentityObservation{}, err
	}
	var response serverless.Response
	if mode == "buffered" {
		if err = json.Unmarshal(data, &response); err != nil {
			return invocationIdentityObservation{}, err
		}
		if response.IsBase64Encoded {
			data, err = base64.StdEncoding.DecodeString(response.Body)
		} else {
			data = []byte(response.Body)
		}
	} else {
		if r.Header.Get("Lambda-Runtime-Function-Response-Mode") != "streaming" || len(r.TransferEncoding) != 1 || r.TransferEncoding[0] != "chunked" {
			return invocationIdentityObservation{}, errors.New("missing real streaming Runtime framing")
		}
		at := bytes.Index(data, make([]byte, 8))
		if at < 0 || at > 16<<10 {
			return invocationIdentityObservation{}, errors.New("invalid streaming prelude delimiter")
		}
		if err = json.Unmarshal(data[:at], &response); err != nil {
			return invocationIdentityObservation{}, err
		}
		data = data[at+8:]
		if r.Trailer.Get("Lambda-Runtime-Function-Error-Type") != "" {
			return invocationIdentityObservation{}, errors.New("unexpected streaming error trailer")
		}
	}
	if err != nil {
		return invocationIdentityObservation{}, err
	}
	if response.StatusCode != 200 || response.Headers["content-encoding"] != "" {
		return invocationIdentityObservation{}, fmt.Errorf("identity response status/encoding: %d %q", response.StatusCode, response.Headers["content-encoding"])
	}
	return invocationIdentityDecode(data)
}
func (c *invocationIdentityController) handle(w http.ResponseWriter, r *http.Request) {
	c.active.Add(1)
	defer c.active.Done()
	defer r.Body.Close()
	if r.URL.Path == "/after" && r.Method == "POST" {
		data, err := invocationIdentityRead(r.Body)
		if err != nil {
			c.fault(err)
			w.WriteHeader(400)
			return
		}
		value, err := invocationIdentityDecode(data)
		if err != nil {
			c.fault(err)
			w.WriteHeader(400)
			return
		}
		index := value.Sequence - 1
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			c.fault(errors.New("callback forwarded credentials"))
		}
		c.mu.Lock()
		accepted := c.accepted[index] || c.mode == "http"
		c.mu.Unlock()
		if !accepted {
			c.fault(errors.New("After began before Runtime accepted publication"))
		}
		select {
		case c.after <- invocationIdentityPublication{index, value}:
		case <-r.Context().Done():
			return
		case <-c.closing:
			return
		}
		select {
		case <-c.finishAfter[index]:
		case <-r.Context().Done():
			return
		case <-c.closing:
			w.WriteHeader(503)
			return
		}
		c.mu.Lock()
		c.callbackReplied[index] = true
		c.mu.Unlock()
		w.WriteHeader(204)
		close(c.afterReplied[index])
		return
	}
	const prefix = "/2018-06-01/runtime/invocation/"
	if r.Method == "GET" && r.URL.Path == prefix+"next" && c.mode != "http" {
		c.mu.Lock()
		index := c.polls
		c.polls++
		previousDone := index == 0 || index <= 3 && c.callbackReplied[index-1]
		c.mu.Unlock()
		if !previousDone {
			c.fault(errors.New("next Runtime poll preceded accepted callback completion"))
		}
		if index == 3 {
			close(c.fourth)
			<-r.Context().Done()
			return
		}
		if index > 3 {
			c.fault(errors.New("unexpected extra warm poll"))
			w.WriteHeader(500)
			return
		}
		requestContext := map[string]any{
			"domainName": "fixture.test",
			"http":       map[string]string{"method": "GET", "path": "/api/identity", "protocol": "HTTP/1.1", "sourceIp": "127.0.0.1"},
		}
		if index < 2 {
			requestContext["requestId"] = []string{"gateway-alpha", "gateway-beta"}[index]
		}
		// Omit requestId entirely on the third event; do not merely send "".
		event := map[string]any{
			"version": "2.0", "rawPath": "/api/identity", "rawQueryString": "sequence=" + strconv.Itoa(index+1),
			"headers": map[string]string{"host": "fixture.test", "accept-encoding": "identity"}, "requestContext": requestContext,
		}
		w.Header().Set("Lambda-Runtime-Aws-Request-Id", []string{"runtime-one", "runtime-two", "runtime-three"}[index])
		w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(20*time.Second).UnixMilli(), 10))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(event)
		return
	}
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, prefix) && c.mode != "http" {
		suffix := strings.TrimPrefix(r.URL.Path, prefix)
		ids := []string{"runtime-one", "runtime-two", "runtime-three"}
		index := -1
		for n, id := range ids {
			if suffix == id+"/response" {
				index = n
			}
		}
		if index < 0 {
			c.fault(fmt.Errorf("unexpected Runtime response/error routing %q", suffix))
			w.WriteHeader(500)
			return
		}
		value, err := invocationIdentityPayload(r, c.mode)
		if err != nil {
			c.fault(err)
			w.WriteHeader(400)
			return
		}
		if value.Sequence != index+1 {
			c.fault(errors.New("Runtime path identity does not match response"))
			w.WriteHeader(400)
			return
		}
		select {
		case c.publication <- invocationIdentityPublication{index, value}:
		case <-r.Context().Done():
			return
		case <-c.closing:
			return
		}
		select {
		case <-c.accept[index]:
		case <-r.Context().Done():
			return
		case <-c.closing:
			w.WriteHeader(503)
			return
		}
		c.mu.Lock()
		c.accepted[index] = true
		c.mu.Unlock()
		w.WriteHeader(202)
		return
	}
	c.fault(fmt.Errorf("unexpected owned controller request %s %s", r.Method, r.URL.Path))
	w.WriteHeader(404)
}
func newInvocationIdentityController(t *testing.T, mode string) *invocationIdentityController {
	t.Helper()
	c := &invocationIdentityController{mode: mode, serveDone: make(chan error, 1), closing: make(chan struct{}), publication: make(chan invocationIdentityPublication, 3), after: make(chan invocationIdentityPublication, 3), fourth: make(chan struct{}), faults: make(chan error, 16)}
	for n := 0; n < 3; n++ {
		c.accept[n] = make(chan struct{})
		c.finishAfter[n] = make(chan struct{})
		c.afterReplied[n] = make(chan struct{})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.origin = "http://" + listener.Addr().String()
	c.server = &http.Server{Handler: http.HandlerFunc(c.handle), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 25 * time.Second}
	go func() { c.serveDone <- c.server.Serve(listener) }()
	t.Cleanup(func() { c.close(t) })
	return c
}
func invocationIdentityStartTicks(pid int) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	at := strings.LastIndex(string(data), ") ")
	if at < 0 {
		return ""
	}
	fields := strings.Fields(string(data[at+2:]))
	if len(fields) <= 19 {
		return ""
	}
	return fields[19]
}
func invocationIdentityBinary(t *testing.T) string {
	t.Helper()
	binary, pin := os.Getenv("NEXTGO_TEST_INVOCATION_BINARY"), os.Getenv("NEXTGO_TEST_INVOCATION_BINARY_SHA256")
	decoded, err := hex.DecodeString(pin)
	if !filepath.IsAbs(binary) || len(decoded) != 32 || err != nil || strings.ToLower(pin) != pin {
		t.Fatal("focused fixture requires absolute NEXTGO_TEST_INVOCATION_BINARY and actual lowercase SHA256")
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 || info.Size() < 1 || info.Size() > 128<<20 {
		t.Fatal("invalid prebuilt invocation executable", err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != pin {
		t.Fatal("prebuilt invocation executable SHA differs")
	}
	if os.Getuid() == 0 {
		t.Fatal("run generated executable fixture as nonroot")
	}
	if expected := os.Getenv("NEXTGO_TEST_UID"); expected != "" && expected != strconv.Itoa(os.Getuid()) {
		t.Fatal("actual test UID differs")
	}
	return binary
}
func (c *invocationIdentityController) start(t *testing.T, binary string) string {
	t.Helper()
	addr := address(t)
	args := []string{"-env-dir", t.TempDir()}
	if c.mode == "http" {
		args = append(args, "-addr", addr)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = "/"
	cmd.WaitDelay = 2 * time.Second
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "AWS_") && !strings.HasPrefix(key, "NEXTGO_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOMAXPROCS=1", "NEXTGO_TEST_INVOCATION_CALLBACK_URL="+c.origin+"/after")
	if c.mode != "http" {
		cmd.Env = append(cmd.Env, "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(c.origin, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+c.mode)
	}
	child := &invocationIdentityChild{cmd: cmd, done: make(chan struct{}), logs: &invocationIdentityLog{}}
	cmd.Stdout = child.logs
	cmd.Stderr = child.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child.start = invocationIdentityStartTicks(cmd.Process.Pid)
	c.child = child
	go func() { child.err = cmd.Wait(); close(child.done) }()
	return "http://" + addr
}
func (c *invocationIdentityController) receive(t *testing.T, source <-chan invocationIdentityPublication) invocationIdentityPublication {
	t.Helper()
	select {
	case value := <-source:
		return value
	case err := <-c.faults:
		t.Fatal(err)
	case <-c.child.done:
		t.Fatalf("child exited before fixture phase: %v %s", c.child.err, c.child.logs.String())
	case <-time.After(10 * time.Second):
		t.Fatal("bounded invocation fixture phase timeout")
	}
	return invocationIdentityPublication{}
}
func (c *invocationIdentityController) assertNoFault(t *testing.T) {
	t.Helper()
	select {
	case err := <-c.faults:
		t.Fatal(err)
	default:
	}
}
func (c *invocationIdentityController) close(t *testing.T) {
	t.Helper()
	c.closeOnce.Do(func() {
		deadline := time.Now().Add(20 * time.Second)
		receipt := map[string]any{"mode": c.mode, "cleanupBudgetSeconds": 20, "childWaitReturned": false, "childKilled": false, "mockServeJoined": false, "mockHandlersJoined": false}
		if child := c.child; child != nil {
			receipt["childPID"] = child.cmd.Process.Pid
			receipt["childLinuxStartTicks"] = child.start
			receipt["startIdentityAvailable"] = child.start != ""
			select {
			case <-child.done:
				t.Error("child exited before requested shutdown", child.err)
			default:
				if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Error(err)
				}
			}
			close(c.closing)
			timer := time.NewTimer(max(time.Until(deadline)-2*time.Second, 0))
			select {
			case <-child.done:
			case <-timer.C:
				receipt["childKilled"] = true
				t.Error("child exceeded shared cleanup budget; forced kill cannot qualify")
				_ = child.cmd.Process.Kill()
			}
			timer.Stop()
			remaining := time.NewTimer(max(time.Until(deadline), 0))
			select {
			case <-child.done:
				receipt["childWaitReturned"] = true
				if child.cmd.ProcessState != nil {
					receipt["childExitCode"] = child.cmd.ProcessState.ExitCode()
				} else {
					t.Error("child Wait returned without an observable process state")
				}
				if child.err != nil {
					t.Error("actual child Wait failed", child.err, child.logs.String())
				}
			case <-remaining.C:
				t.Error("child physical Wait did not join before absolute cleanup deadline")
			}
			remaining.Stop()
		} else {
			close(c.closing)
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		if err := c.server.Shutdown(ctx); err != nil {
			t.Error("owned mock HTTP shutdown", err)
			_ = c.server.Close()
		}
		select {
		case err := <-c.serveDone:
			receipt["mockServeJoined"] = true
			if !errors.Is(err, http.ErrServerClosed) {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("owned mock Serve did not join")
		}
		handlersDone := make(chan struct{})
		go func() { c.active.Wait(); close(handlersDone) }()
		select {
		case <-handlersDone:
			receipt["mockHandlersJoined"] = true
		case <-ctx.Done():
			t.Error("owned mock handlers did not join")
		}
		c.mu.Lock()
		receipt["runtimePolls"] = c.polls
		receipt["runtimeAccepted"] = c.accepted
		receipt["callbackReplies"] = c.callbackReplied
		c.mu.Unlock()
		for {
			select {
			case err := <-c.faults:
				t.Error("owned controller fault", err)
			default:
				goto faultsDrained
			}
		}
	faultsDrained:
		data, _ := json.Marshal(receipt)
		t.Log("INVOCATION_EXECUTABLE_RECEIPT " + string(data))
	})
}
func invocationIdentityExpected(mode string, index int) invocationIdentityObservation {
	value := invocationIdentityObservation{Sequence: index + 1}
	if mode != "http" {
		value.Invocation = []string{"runtime-one", "runtime-two", "runtime-three"}[index]
		if index < 2 {
			value.Gateway = []string{"gateway-alpha", "gateway-beta"}[index]
		}
		value.RetainedInvocation = "runtime-one"
		value.RetainedGateway = "gateway-alpha"
	}
	return value
}
func TestInvocationIdentityGeneratedExecutable(t *testing.T) {
	binary := invocationIdentityBinary(t)
	for _, mode := range []string{"http", "buffered", "streaming"} {
		if !t.Run(mode, func(t *testing.T) {
			controller := newInvocationIdentityController(t, mode)
			origin := controller.start(t, binary)
			if mode == "http" {
				client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true}}
				defer client.CloseIdleConnections()
				until := time.Now().Add(7 * time.Second)
				for {
					response, err := client.Get(origin + "/")
					if err == nil {
						data, readErr := invocationIdentityRead(response.Body)
						response.Body.Close()
						if readErr == nil && response.StatusCode == 200 && bytes.Contains(data, []byte("identity executable ready")) {
							break
						}
					}
					if time.Now().After(until) {
						t.Fatal("native generated server not ready")
					}
					select {
					case <-controller.child.done:
						t.Fatal("child exited during readiness", controller.child.err)
					case <-time.After(10 * time.Millisecond):
					}
				}
				for index := 0; index < 3; index++ {
					response, err := client.Get(origin + "/api/identity?sequence=" + strconv.Itoa(index+1))
					if err != nil {
						t.Fatal(err)
					}
					data, err := invocationIdentityRead(response.Body)
					response.Body.Close()
					if err != nil || response.StatusCode != 200 {
						t.Fatal(response.StatusCode, err)
					}
					value, err := invocationIdentityDecode(data)
					if err != nil || value != invocationIdentityExpected(mode, index) {
						t.Fatal("native HTTP must have no invocation or gateway IDs", value, err)
					}
					callback := controller.receive(t, controller.after)
					if callback.index != index || callback.value != value {
						t.Fatal("native After snapshot differs", callback, value)
					}
					data, _ = json.Marshal(map[string]any{"mode": mode, "responseAndAfter": callback.value})
					t.Log("INVOCATION_EXECUTABLE_OBSERVATION " + string(data))
					close(controller.finishAfter[index])
					select {
					case <-controller.afterReplied[index]:
					case err := <-controller.faults:
						t.Fatal(err)
					case <-controller.child.done:
						t.Fatal("child exited before callback reply", controller.child.err)
					case <-time.After(10 * time.Second):
						t.Fatal("callback handler did not reach its accepted reply")
					}
					controller.assertNoFault(t)
				}
			} else {
				for index := 0; index < 3; index++ {
					publication := controller.receive(t, controller.publication)
					if publication.index != index || publication.value != invocationIdentityExpected(mode, index) {
						t.Fatal("warm Runtime/gateway/retained-first identity mismatch", publication)
					}
					select {
					case early := <-controller.after:
						t.Fatal("callback before accepted publication", early)
					default:
					}
					controller.mu.Lock()
					polls := controller.polls
					controller.mu.Unlock()
					if polls != index+1 {
						t.Fatal("polled next event while response not accepted", polls)
					}
					close(controller.accept[index])
					callback := controller.receive(t, controller.after)
					if callback.index != index || callback.value != publication.value {
						t.Fatal("accepted After identity snapshot differs", callback, publication)
					}
					controller.mu.Lock()
					polls = controller.polls
					controller.mu.Unlock()
					if polls != index+1 {
						t.Fatal("polled next event while accepted callback held", polls)
					}
					data, _ := json.Marshal(map[string]any{"mode": mode, "responseAndAfter": callback.value})
					t.Log("INVOCATION_EXECUTABLE_OBSERVATION " + string(data))
					close(controller.finishAfter[index])
					select {
					case <-controller.afterReplied[index]:
					case err := <-controller.faults:
						t.Fatal(err)
					case <-controller.child.done:
						t.Fatal("child exited before callback reply", controller.child.err)
					case <-time.After(10 * time.Second):
						t.Fatal("callback handler did not reach its accepted reply")
					}
					controller.assertNoFault(t)
				}
				select {
				case <-controller.fourth:
				case err := <-controller.faults:
					t.Fatal(err)
				case <-controller.child.done:
					t.Fatal("child exited before next warm poll", controller.child.err)
				case <-time.After(10 * time.Second):
					t.Fatal("no next poll after final accepted callback")
				}
			}
			controller.assertNoFault(t)
		}) {
			return
		}
	}
}
