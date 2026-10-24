//go:build deployment

package deployment

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/serverless"
	"github.com/Suhaibinator/Next.go/ui"
)

// A self-executing fixture also runs in the source/compiler-free deployment
// container. The controller holds a deliberately noncooperative physical loader.
func TestStreamLimitsProcessHelper(t *testing.T) {
	if os.Getenv("NEXTGO_TEST_STREAM_HELPER") != "true" {
		return
	}
	app, err := nextgo.New(nextgo.Config{BuildID: "stream-limits-fixture", Logger: log.New(io.Discard, "", 0), Streaming: nextgo.StreamingOptions{MaxStreams: 1, MaxLoaders: 1, Timeout: 2 * time.Second}, Routes: []nextgo.Route{
		{Pattern: "/health", API: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }},
		{Pattern: "/immediate", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Stream: true, Body: ui.Text("immediate stream accepted")}, nil
		}},
		{Pattern: "/stream", Page: func(c *nextgo.Context) (nextgo.Page, error) {
			id := c.Request.URL.Query().Get("id")
			return nextgo.Page{Stream: true, Title: "Limited stream", Body: ui.Defer("fixture-boundary", ui.Text("stream pending"), func(context.Context) (ui.Node, error) {
				// No request context: cancellation must not release a physical token.
				client := &http.Client{Timeout: 10 * time.Second}
				defer client.CloseIdleConnections()
				response, err := client.Get(os.Getenv("NEXTGO_TEST_STREAM_CONTROLLER") + "/?id=" + id)
				if err != nil {
					return nil, err
				}
				defer response.Body.Close()
				_, err = io.Copy(io.Discard, response.Body)
				return ui.Text("loader completed " + id), err
			})}, nil
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	listener, err := net.Listen("tcp", os.Getenv("NEXTGO_TEST_STREAM_ADDR"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		err = serverless.Run(ctx, app)
	} else {
		select {
		case <-ctx.Done():
		case err = <-done:
		}
	}
	drain, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(drain)
	_ = app.Close(drain)
	if err != nil && ctx.Err() == nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

type streamGate struct {
	server  *httptest.Server
	entered chan string
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newStreamGate(t *testing.T) *streamGate {
	t.Helper()
	g := &streamGate{entered: make(chan string, 16), release: make(chan struct{})}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		g.calls.Add(1)
		g.entered <- id
		if id == "held" {
			select {
			case <-g.release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(func() { g.unblock(); g.server.Close() })
	return g
}
func (g *streamGate) unblock() { g.once.Do(func() { close(g.release) }) }
func (g *streamGate) wait(t *testing.T, id string) {
	t.Helper()
	select {
	case got := <-g.entered:
		if got != id {
			t.Fatal("unexpected physical loader", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loader did not enter", id)
	}
}

func streamLimitsChild(t *testing.T, g *streamGate, extra ...string) (*process, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	addr := address(t)
	cmd := exec.Command(binary, "-test.run=^TestStreamLimitsProcessHelper$", "-test.timeout=30s")
	cmd.Dir = "/"
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "NEXTGO_TEST_STREAM_") && !strings.HasPrefix(key, "AWS_LAMBDA_") && !strings.HasPrefix(key, "NEXTGO_LAMBDA_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "NEXTGO_TEST_STREAM_HELPER=true", "NEXTGO_TEST_STREAM_ADDR="+addr, "NEXTGO_TEST_STREAM_CONTROLLER="+g.server.URL)
	cmd.Env = append(cmd.Env, extra...)
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
	origin := "http://" + addr
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(origin + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 204 {
				return p, origin
			}
		}
		select {
		case <-p.done:
			t.Fatal(p.err, p.logs.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("stream helper readiness timeout", p.logs.String())
	return nil, ""
}

func streamLimitsRequest(t *testing.T, client *http.Client, origin, method, path string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, origin+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, data
}
func streamLimitsSaturation(t *testing.T, client *http.Client, origin string) {
	t.Helper()
	for _, tc := range []struct {
		method  string
		headers map[string]string
	}{{"GET", nil}, {"HEAD", nil}, {"GET", map[string]string{"X-Nextgo-Navigate": "1"}}, {"GET", map[string]string{"X-Nextgo-Navigate": "1", "X-Nextgo-Stream": "1"}}} {
		status, h, payload := streamLimitsRequest(t, client, origin, tc.method, "/stream?id=forbidden", tc.headers)
		if status != 503 || h.Get("Retry-After") != "1" || h.Get("Cache-Control") != "private, no-store" || h.Get("X-Nextgo-Stream") != "" || strings.Contains(string(payload), "stream pending") {
			t.Fatal("invalid admission rejection", status, h, string(payload))
		}
		if tc.headers["X-Nextgo-Navigate"] == "1" {
			var envelope struct {
				Status int    `json:"status"`
				HTML   string `json:"html"`
			}
			if json.Unmarshal(payload, &envelope) != nil || envelope.Status != 503 || envelope.HTML == "" {
				t.Fatal("missing recoverable navigation rejection", string(payload))
			}
		}
	}
	status, _, _ := streamLimitsRequest(t, client, origin, "GET", "/stream?id=prefetch", map[string]string{"X-Nextgo-Navigate": "1", "X-Nextgo-Purpose": "prefetch"})
	if status != 204 {
		t.Fatal("prefetch consumed stream admission", status)
	}
}

func TestStreamLimitsHTTPExecutable(t *testing.T) {
	g := newStreamGate(t)
	p, origin := streamLimitsChild(t, g)
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", origin+"/stream?id=held", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	g.wait(t, "held")
	streamLimitsSaturation(t, client, origin)
	if g.calls.Load() != 1 {
		t.Fatal("rejected render invoked loader", g.calls.Load())
	}
	cancel()
	response.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, _, payload := streamLimitsRequest(t, client, origin, "GET", "/immediate", nil)
		if status == 200 && strings.Contains(string(payload), "immediate stream accepted") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation retained stream admission", status, p.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// This request admits a stream but must not start another physical callback.
	_, _, payload := streamLimitsRequest(t, client, origin, "GET", "/stream?id=blocked", map[string]string{"X-Nextgo-Navigate": "1"})
	if g.calls.Load() != 1 || strings.Contains(string(payload), "loader completed") {
		t.Fatal("canceled callback released physical loader token", g.calls.Load(), string(payload))
	}
	g.unblock()
	status, _, payload := streamLimitsRequest(t, client, origin, "GET", "/stream?id=reused", map[string]string{"X-Nextgo-Navigate": "1"})
	if status != 200 || !strings.Contains(string(payload), "loader completed reused") {
		t.Fatal("physical token not reusable after callback return", status, string(payload), p.logs.String())
	}
	g.wait(t, "reused")
}

func streamLimitsLambdaEnvelope(r *http.Request, mode string) (httpEnvelope, []byte, error) {
	var envelope httpEnvelope
	if mode == "buffered" {
		if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&envelope); err != nil {
			return envelope, nil, err
		}
		payload := []byte(envelope.Body)
		if envelope.Base64 {
			data, err := base64.StdEncoding.DecodeString(envelope.Body)
			return envelope, data, err
		}
		return envelope, payload, nil
	}
	reader := bufio.NewReader(io.LimitReader(r.Body, 2<<20))
	var prelude []byte
	for len(prelude) < 16<<10 {
		value, err := reader.ReadByte()
		if err != nil {
			return envelope, nil, err
		}
		prelude = append(prelude, value)
		if len(prelude) >= 8 && strings.HasSuffix(string(prelude), strings.Repeat("\x00", 8)) {
			prelude = prelude[:len(prelude)-8]
			break
		}
	}
	if err := json.Unmarshal(prelude, &envelope); err != nil {
		return envelope, nil, err
	}
	payload, err := io.ReadAll(reader)
	return envelope, payload, err
}

func TestStreamLimitsLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			g := newStreamGate(t)
			var issued atomic.Int32
			reports := make(chan struct{}, 4)
			failures := make(chan error, 8)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= 4 {
						<-r.Context().Done()
						return
					}
					paths := []string{"/stream?id=held", "/immediate", "/stream?id=blocked", "/stream?id=reused"}
					if index == 3 {
						g.unblock()
					}
					path, query, _ := strings.Cut(paths[index], "?")
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": path, "rawQueryString": query, "headers": map[string]string{"host": "stream.test", "x-nextgo-navigate": "1", "x-nextgo-stream": "1"}, "requestContext": map[string]any{"domainName": "stream.test", "http": map[string]string{"method": "GET", "path": path, "sourceIp": "127.0.0.1"}}})
					return
				}
				parts := strings.Split(r.URL.Path, "/")
				if len(parts) < 3 || parts[len(parts)-1] != "response" {
					failures <- fmt.Errorf("unexpected invocation report %s", r.URL.Path)
					w.WriteHeader(202)
					reports <- struct{}{}
					return
				}
				index, err := strconv.Atoi(parts[len(parts)-2])
				if err != nil {
					failures <- err
					w.WriteHeader(202)
					return
				}
				envelope, payload, err := streamLimitsLambdaEnvelope(r, mode)
				if err != nil {
					failures <- err
				}
				if index == 0 {
					if g.calls.Load() != 1 {
						failures <- fmt.Errorf("held loader not physical at completion: %d", g.calls.Load())
					}
				}
				if index == 0 || index == 2 {
					if envelope.StatusCode != 200 || envelope.Headers["x-nextgo-stream"] != "1" || !strings.Contains(string(payload), "stream pending") || strings.Contains(string(payload), `"type":"done"`) || strings.Contains(string(payload), "loader completed") || g.calls.Load() != 1 {
						failures <- fmt.Errorf("deadline did not retain physical admission: %d %s calls%d", envelope.StatusCode, payload, g.calls.Load())
					}
				}
				if index == 1 && (envelope.StatusCode != 200 || !strings.Contains(string(payload), "immediate stream accepted") || g.calls.Load() != 1) {
					failures <- fmt.Errorf("stream admission not reusable: %d %s", envelope.StatusCode, payload)
				}
				if index == 3 && (envelope.StatusCode != 200 || !strings.Contains(string(payload), "loader completed reused") || g.calls.Load() != 2) {
					failures <- fmt.Errorf("physical loader not reusable: %d %s calls%d", envelope.StatusCode, payload, g.calls.Load())
				}
				w.WriteHeader(202)
				reports <- struct{}{}
			}))
			defer runtime.Close()
			p, base := streamLimitsChild(t, g, "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
			defer func() {
				select {
				case <-p.done:
					return
				default:
				}
				_ = p.cmd.Process.Kill()
				<-p.done
			}()
			g.wait(t, "held")
			// The same App also serves HTTP, so a Lambda render must consume its
			// shared stream admission before that invocation has completed.
			streamLimitsSaturation(t, &http.Client{Timeout: time.Second}, base)
			for i := 0; i < 4; i++ {
				select {
				case <-reports:
				case <-p.done:
					t.Fatal(p.err, p.logs.String())
				case <-time.After(10 * time.Second):
					t.Fatal("stream limits invocation timeout", p.logs.String())
				}
			}
			select {
			case err := <-failures:
				t.Fatal(err, p.logs.String())
			default:
			}
			g.wait(t, "reused")
		})
	}
}
