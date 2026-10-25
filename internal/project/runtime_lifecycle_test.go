package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func lifecycleProject() *Project {
	return &Project{Import: "lifecycle.fixture", Configure: "lifecycle.fixture/app", Routes: []Route{{Pattern: "/", Import: "lifecycle.fixture/app"}}}
}

func lifecycleSource(aggregate, dev bool) string {
	p := lifecycleProject()
	if aggregate {
		return zonesServerSource([]compiledZone{{ZoneManifest{ID: "site", Host: "site.test"}, p, "lifecycle-fixture"}}, dev)
	}
	return serverSource(p, "lifecycle-fixture", dev)
}

func TestGeneratedOrdinaryAndAggregateServersOwnApplicationCleanup(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		for _, dev := range []bool{false, true} {
			t.Run(fmt.Sprintf("aggregate=%t/dev=%t", aggregate, dev), func(t *testing.T) {
				source := lifecycleSource(aggregate, dev)
				constructor := strings.Index(source, "app,err:=nextgo.New")
				drain := strings.Index(source, "defer func(){drain,cancel:=")
				lambda := strings.Index(source, `if os.Getenv("AWS_LAMBDA_RUNTIME_API")`)
				if constructor < 0 || drain <= constructor || lambda <= drain {
					t.Fatal("constructed application is not owned before early returns or Lambda")
				}
				if !strings.Contains(source, "func main(){if err:=run();err!=nil{log.Print(err);os.Exit(1)}}") || !strings.Contains(source, "func run()error") {
					t.Fatal("process exits before generated run can unwind")
				}
				tail := source[constructor:]
				if strings.Contains(tail, "log.Fatal(") {
					t.Fatal("fatal exit can bypass constructed application cleanup")
				}
				join := "defer func(){stop();<-shutdownDone}()"
				if aggregate {
					join = "defer func(){stop();<-done}()"
				}
				if i := strings.Index(source, join); i < lambda || i > strings.Index(source, "server.ListenAndServe()") {
					t.Fatal("HTTP startup failure can retain its shutdown goroutine")
				}
				if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
					t.Fatal("invalid generated lifecycle", err)
				}
			})
		}
	}
}

const lifecycleFixtureApp = `package app
import("context";"fmt";"net/http";"os";"strings";nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/telemetry";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("lifecycle fixture")},nil}
func Configure(c *nextgo.Config){
 c.Handlers=map[string]http.Handler{
  "/ready":http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)}),
  "/hold":http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
   control:=strings.TrimSuffix(os.Getenv("NEXTGO_TEST_LIFECYCLE_CLOSE_URL"),"/close")+"/request"
   req,err:=http.NewRequestWithContext(r.Context(),http.MethodGet,control,nil);if err!=nil{http.Error(w,"request control failed",500);return}
   response,err:=http.DefaultClient.Do(req);if err!=nil{http.Error(w,"request control failed",500);return};response.Body.Close()
   if response.StatusCode!=http.StatusNoContent{http.Error(w,"request control failed",500);return}
   fmt.Fprint(w,"held response completed")
  }),
 }
 var err error
 c.Tracing,err=telemetry.New(telemetry.Options{Recorder:func(telemetry.Record)error{return nil},Shutdown:func(ctx context.Context)error{
  req,err:=http.NewRequestWithContext(ctx,http.MethodGet,os.Getenv("NEXTGO_TEST_LIFECYCLE_CLOSE_URL"),nil);if err!=nil{return err}
  response,err:=http.DefaultClient.Do(req);if err!=nil{return err};response.Body.Close()
  if response.StatusCode!=http.StatusNoContent{return fmt.Errorf("cleanup control status %d",response.StatusCode)}
  f,err:=os.OpenFile(os.Getenv("NEXTGO_TEST_LIFECYCLE_RECORD"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{return err}
  _,writeErr:=f.WriteString("closed\n");closeErr:=f.Close();if writeErr!=nil{return writeErr};return closeErr
 }})
 if err!=nil{panic(err)}
 if os.Getenv("NEXTGO_TEST_LIFECYCLE_CLOSE_STDOUT")=="true"{if err:=os.Stdout.Close();err!=nil{panic(err)}}
}
`

// Compile actual generated native startup directly. Runtime bytes are inert
// fixture files: these checks never compile or execute a WASM application.
func buildLifecycleFixture(t *testing.T, aggregate, dev bool) string {
	t.Helper()
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeInput(t, root, "go.mod", fmt.Sprintf("module lifecycle.fixture\n\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(frameworkRoot)))
	writeInput(t, root, "app/app.go", lifecycleFixtureApp)
	assetRoot, privateRoot := "entry/assets/public", "entry/private"
	if aggregate {
		assetRoot, privateRoot = "entry/assets/site/public", "entry/private/site"
	}
	writeInput(t, root, assetRoot+"/placeholder", "asset")
	writeInput(t, root, privateRoot+"/prerender.json", "{}")
	if err := writeGo(filepath.Join(root, "entry/main.go"), lifecycleSource(aggregate, dev)); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "server")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./entry")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile generated lifecycle: %v\n%s", err, output)
	}
	return binary
}

type lifecycleCapture struct {
	mu sync.Mutex
	bytes.Buffer
}

func (c *lifecycleCapture) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Buffer.Write(b)
}

func (c *lifecycleCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Buffer.String()
}

func runLifecycleProbe(t *testing.T, binary string, success bool, environment []string, args ...string) (string, string) {
	t.Helper()
	record := filepath.Join(t.TempDir(), "closed")
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/close" {
			w.WriteHeader(http.StatusInternalServerError) // local Lambda /next failure
			return
		}
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(control.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_LAMBDA_RESPONSE_MODE=", "NEXTGO_ENV_DIR=", "NEXTGO_ZONE_HOSTS=", "NEXTGO_TEST_LIFECYCLE_CLOSE_STDOUT=", "NEXTGO_TEST_LIFECYCLE_RECORD="+record, "NEXTGO_TEST_LIFECYCLE_CLOSE_URL="+control.URL+"/close")
	for _, entry := range environment {
		if entry == "AWS_LAMBDA_RUNTIME_API=local" {
			entry = "AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(control.URL, "http://")
		}
		cmd.Env = append(cmd.Env, entry)
	}
	var stdout, stderr lifecycleCapture
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})
	select {
	case <-entered:
	case <-done:
		t.Fatalf("process exited without application cleanup: %v\n%s", waitErr, stderr.String())
	case <-time.After(3 * time.Second):
		t.Fatalf("application cleanup did not start\n%s", stderr.String())
	}
	// The hook is deliberately held on a request, not by the production drain
	// timeout. Exiting while it is held would abandon an owned native resource.
	select {
	case <-done:
		t.Fatalf("process exited before application cleanup completed: %v\n%s", waitErr, stderr.String())
	case <-time.After(25 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("process did not join completed cleanup\n%s", stderr.String())
	}
	if (waitErr == nil) != success {
		t.Fatalf("unexpected process result: %v\n%s", waitErr, stderr.String())
	}
	closed, err := os.ReadFile(record)
	if err != nil || string(closed) != "closed\n" {
		t.Fatalf("shutdown hook must complete exactly once: %q, %v\n%s", closed, err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestGeneratedServerStartupAndShutdownCloseOwnedResources(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		for _, dev := range []bool{false, true} {
			t.Run(fmt.Sprintf("aggregate=%t/dev=%t", aggregate, dev), func(t *testing.T) {
				binary := buildLifecycleFixture(t, aggregate, dev)
				t.Run("signal-drains-request-before-cleanup", func(t *testing.T) {
					runLifecycleShutdownProbe(t, binary)
				})
				t.Run("occupied-port", func(t *testing.T) {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer listener.Close()
					_, stderr := runLifecycleProbe(t, binary, false, nil, "-addr", listener.Addr().String())
					if !strings.Contains(stderr, "listen tcp") {
						t.Fatal("original listener failure was discarded", stderr)
					}
				})
				for _, mode := range []string{"buffered", "streaming"} {
					t.Run("lambda-"+mode+"-failure", func(t *testing.T) {
						_, stderr := runLifecycleProbe(t, binary, false, []string{"AWS_LAMBDA_RUNTIME_API=local", "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
						if !strings.Contains(stderr, "next invocation HTTP 500") {
							t.Fatal("original Runtime API failure was discarded", stderr)
						}
					})
				}
				if !aggregate {
					t.Run("inspection-success", func(t *testing.T) {
						stdout, _ := runLifecycleProbe(t, binary, true, nil, "-path-config")
						if !json.Valid([]byte(stdout)) {
							t.Fatal("cleanup contaminated JSON inspection output", stdout)
						}
					})
					t.Run("inspection-output-failure", func(t *testing.T) {
						_, stderr := runLifecycleProbe(t, binary, false, []string{"NEXTGO_TEST_LIFECYCLE_CLOSE_STDOUT=true"}, "-path-config")
						if !strings.Contains(stderr, "file already closed") {
							t.Fatal("original inspection write failure was discarded", stderr)
						}
					})
					// Static publication deliberately rejects Dev before constructing
					// an application; only production owns a post-construction export.
					if !dev {
						t.Run("export-failure", func(t *testing.T) {
							_, stderr := runLifecycleProbe(t, binary, false, nil, "-export-config", "-export-domain", "unconfigured.test")
							if !strings.Contains(stderr, "static export domain requires configured locale domains") {
								t.Fatal("original export failure was discarded", stderr)
							}
						})
					}
				}
			})
		}
	}
}

func runLifecycleShutdownProbe(t *testing.T, binary string) {
	t.Helper()
	record := filepath.Join(t.TempDir(), "closed")
	requestEntered, closeEntered := make(chan struct{}), make(chan struct{})
	requestRelease, closeRelease := make(chan struct{}), make(chan struct{})
	var requestEnteredOnce, closeEnteredOnce, requestReleaseOnce, closeReleaseOnce sync.Once
	releaseRequest := func() { requestReleaseOnce.Do(func() { close(requestRelease) }) }
	releaseClose := func() { closeReleaseOnce.Do(func() { close(closeRelease) }) }
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var release <-chan struct{}
		switch r.URL.Path {
		case "/request":
			requestEnteredOnce.Do(func() { close(requestEntered) })
			release = requestRelease
		case "/close":
			closeEnteredOnce.Do(func() { close(closeEntered) })
			release = closeRelease
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(control.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, binary, "-addr", address)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_LAMBDA_RESPONSE_MODE=", "NEXTGO_ENV_DIR=", "NEXTGO_ZONE_HOSTS=", "NEXTGO_TEST_LIFECYCLE_CLOSE_STDOUT=", "NEXTGO_TEST_LIFECYCLE_RECORD="+record, "NEXTGO_TEST_LIFECYCLE_CLOSE_URL="+control.URL+"/close")
	var logs lifecycleCapture
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	requestDone := make(chan struct{})
	requestStarted := false
	var requestErr error
	var requestStatus int
	var requestBody string
	t.Cleanup(func() {
		// Every failure releases both parent barriers, then cancels/joins the
		// independent client and child before closing their control server.
		releaseRequest()
		releaseClose()
		cancelRequest()
		cancel()
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
		if requestStarted {
			<-requestDone
		}
		client.CloseIdleConnections()
	})
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelReady()
	for {
		req, err := http.NewRequestWithContext(readyCtx, http.MethodGet, "http://"+address+"/ready", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "site.test"
		response, err := client.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				break
			}
		}
		select {
		case <-done:
			t.Fatalf("server exited before listening: %v\n%s", waitErr, logs.String())
		case <-readyCtx.Done():
			t.Fatalf("server did not become ready\n%s", logs.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	requestStarted = true
	go func() {
		defer close(requestDone)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+address+"/hold", nil)
		if err != nil {
			requestErr = err
			return
		}
		req.Host = "site.test"
		response, err := client.Do(req)
		if err != nil {
			requestErr = err
			return
		}
		defer response.Body.Close()
		requestStatus = response.StatusCode
		data, err := io.ReadAll(io.LimitReader(response.Body, 256))
		requestBody = string(data)
		requestErr = err
	}()
	select {
	case <-requestEntered:
	case <-requestDone:
		t.Fatalf("held request finished before SIGTERM: %v %d %q\n%s", requestErr, requestStatus, requestBody, logs.String())
	case <-done:
		t.Fatalf("server exited before request entry: %v\n%s", waitErr, logs.String())
	case <-readyCtx.Done():
		t.Fatalf("held request did not enter\n%s", logs.String())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// One overall three-second observation budget keeps both parent barriers
	// well inside the unchanged shared five-second production shutdown budget.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelDrain()
	select {
	case <-closeEntered:
		t.Fatal("application cleanup began before the active request drained")
	case <-requestDone:
		t.Fatal("active request was canceled instead of drained")
	case <-done:
		t.Fatalf("server exited with an active request: %v\n%s", waitErr, logs.String())
	case <-drainCtx.Done():
		t.Fatalf("request drain observation exceeded its budget\n%s", logs.String())
	case <-time.After(25 * time.Millisecond):
	}
	releaseRequest()
	select {
	case <-requestDone:
		if requestErr != nil || requestStatus != http.StatusOK || requestBody != "held response completed" {
			t.Fatalf("active request did not finish its response: %v %d %q\n%s", requestErr, requestStatus, requestBody, logs.String())
		}
	case <-done:
		t.Fatalf("server exited before its active response completed: %v\n%s", waitErr, logs.String())
	case <-drainCtx.Done():
		t.Fatalf("active request did not drain\n%s", logs.String())
	}
	select {
	case <-closeEntered:
	case <-done:
		t.Fatalf("server exited before cleanup entry: %v\n%s", waitErr, logs.String())
	case <-drainCtx.Done():
		t.Fatalf("application cleanup did not start after request drain\n%s", logs.String())
	}
	select {
	case <-done:
		t.Fatalf("server exited before owned cleanup completed: %v\n%s", waitErr, logs.String())
	case <-drainCtx.Done():
		t.Fatalf("cleanup observation exceeded its budget\n%s", logs.String())
	case <-time.After(25 * time.Millisecond):
	}
	releaseClose()
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("graceful shutdown failed: %v\n%s", waitErr, logs.String())
		}
	case <-drainCtx.Done():
		t.Fatalf("server did not join graceful cleanup\n%s", logs.String())
	}
	closed, err := os.ReadFile(record)
	if err != nil || string(closed) != "closed\n" {
		t.Fatalf("shutdown hook must complete exactly once: %q, %v\n%s", closed, err, logs.String())
	}
}
