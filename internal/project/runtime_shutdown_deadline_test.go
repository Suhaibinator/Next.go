package project

import (
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

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

func TestGeneratedHTTPDrainUsesIndependentApplicationCleanup(t *testing.T) {
	for name, source := range map[string]string{
		"ordinary":  serverSource(lifecycleProject(), "deadline-fixture", false),
		"aggregate": lifecycleSource(true, false),
		"release":   zoneReleaseServerSource(&Project{}, &zoneReleasePlan{}),
		"ingress":   zoneIngressServerSource([]byte(`{}`)),
	} {
		t.Run(name, func(t *testing.T) {
			owner := strings.Index(source, "defer func(){drain,cancel:=context.WithTimeout(context.Background(),5*time.Second)")
			lambda := strings.Index(source, `if os.Getenv("AWS_LAMBDA_RUNTIME_API")`)
			join := strings.Index(source, "defer func(){stop();<-")
			listen := strings.Index(source, "server.ListenAndServe()")
			if owner < 0 || lambda <= owner || join <= lambda || listen <= join {
				t.Fatal("HTTP worker must join before unconditional application cleanup unwinds")
			}
			if strings.Count(source, "app.Close(") != 1 || !strings.Contains(source, "app.Close(drain)") {
				t.Fatal("application cleanup must receive one independent deadline on every return")
			}
			if !strings.Contains(source, "if err:=server.Shutdown(") || !strings.Contains(source, "server.Close()") {
				t.Fatal("expired HTTP drain must close remaining connections")
			}
			if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

const deadlineFixtureApp = `package app
import("context";"fmt";"net/http";"os";"time";nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/telemetry";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("deadline fixture")},nil}
func appendRecord(line string)error{f,err:=os.OpenFile(os.Getenv("NEXTGO_TEST_DEADLINE_RECORD"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{return err};_,writeErr:=fmt.Fprintln(f,line);closeErr:=f.Close();if writeErr!=nil{return writeErr};return closeErr}
func control(ctx context.Context,path string)error{req,err:=http.NewRequestWithContext(ctx,http.MethodGet,os.Getenv("NEXTGO_TEST_DEADLINE_CONTROL")+path,nil);if err!=nil{return err};response,err:=http.DefaultClient.Do(req);if err!=nil{return err};response.Body.Close();if response.StatusCode!=http.StatusNoContent{return fmt.Errorf("control status %d",response.StatusCode)};return nil}
func Configure(c *nextgo.Config){
 c.Handlers=map[string]http.Handler{
  "/ready":http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)}),
  "/hold":http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
   if err:=control(r.Context(),"/entered");err!=nil{return}
   <-r.Context().Done()
   if err:=appendRecord("request-canceled");err!=nil{return}
   report,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel();_ = control(report,"/canceled")
  }),
 }
 var err error
 c.Tracing,err=telemetry.New(telemetry.Options{Recorder:func(telemetry.Record)error{return nil},Shutdown:func(ctx context.Context)error{
  if err:=appendRecord(fmt.Sprintf("close-context:%v",ctx.Err()));err!=nil{return err}
  if err:=control(ctx,"/cleanup");err!=nil{return err}
  return appendRecord("close-ack")
 }})
 if err!=nil{panic(err)}
}
`

// ZoneIngress owns no application tracer. This test-only observer wraps the real
// constructor result while retaining its actual Handler/Close implementation.
// The emitted HTTP loop and cleanup code are compiled unchanged. The observer
// records the context passed to Close and requires a real control HTTP exchange.
const deadlineIngressObserver = `
type deadlineObservedIngress struct{*nextgo.ZoneIngress;held chan context.Context}
func (app *deadlineObservedIngress) ServeHTTP(w http.ResponseWriter,r *http.Request){if r.URL.Path=="/hold"{select{case app.held<-r.Context():default:}};app.ZoneIngress.ServeHTTP(w,r)}
func deadlineAppend(line string)error{f,err:=os.OpenFile(os.Getenv("NEXTGO_TEST_DEADLINE_RECORD"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{return err};_,writeErr:=fmt.Fprintln(f,line);closeErr:=f.Close();if writeErr!=nil{return writeErr};return closeErr}
func (app *deadlineObservedIngress) Close(ctx context.Context)(result error){
 closed:=false;closeReal:=func()error{closed=true;return app.ZoneIngress.Close(ctx)}
 defer func(){if !closed{if err:=closeReal();result==nil{result=err}}}()
 if err:=deadlineAppend(fmt.Sprintf("close-context:%v",ctx.Err()));err!=nil{return err}
 select{case held:=<-app.held:select{case <-held.Done():if held.Err()!=context.Canceled{return fmt.Errorf("unexpected held request cancellation: %v",held.Err())};if err:=deadlineAppend("downstream-canceled-before-close");err!=nil{return err};case <-ctx.Done():return ctx.Err()};default:return fmt.Errorf("missing held request context")}
 if err:=closeReal();err!=nil{return err}
 req,err:=http.NewRequestWithContext(ctx,http.MethodGet,os.Getenv("NEXTGO_TEST_DEADLINE_CONTROL")+"/cleanup",nil);if err!=nil{return err}
 response,err:=http.DefaultClient.Do(req);if err!=nil{return err};response.Body.Close();if response.StatusCode!=http.StatusNoContent{return fmt.Errorf("control status %d",response.StatusCode)}
 return deadlineAppend("close-ack")
}
`

type deadlineNativeFixture struct {
	binary      string
	args        []string
	expectation *zonecontract.Expectation
}

// These fixtures compile native startup only. Release runtime files are inert
// nonempty bytes admitted by the real frozen-asset contract; no WASM is built.
func buildDeadlineNativeFixture(t *testing.T, family, upstream string) deadlineNativeFixture {
	t.Helper()
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeInput(t, root, "go.mod", fmt.Sprintf("module lifecycle.fixture\n\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(frameworkRoot)))
	writeInput(t, root, "app/app.go", deadlineFixtureApp)
	result := deadlineNativeFixture{}
	var source string
	switch family {
	case "ordinary":
		writeInput(t, root, "entry/assets/public/placeholder", "asset")
		writeInput(t, root, "entry/private/prerender.json", "{}")
		source = lifecycleSource(false, false)
	case "aggregate":
		writeInput(t, root, "entry/assets/site/public/placeholder", "asset")
		writeInput(t, root, "entry/private/site/prerender.json", "{}")
		source = lifecycleSource(true, false)
	case "release", "ingress":
		runtimeID := strings.Repeat("a", 20)
		spec := zonecontract.ReleaseSpec{Version: zonecontract.Version, Zone: zonecontract.Binding{ID: "site", Host: "site.test"}, ReleaseID: "deadline", RuntimeID: runtimeID, PublicOrigin: "http://site.test", AssetPrefix: "/_nextgo/releases/deadline", AssetsSHA256: strings.Repeat("b", 64), TopologySHA256: strings.Repeat("c", 64), NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol}
		if family == "release" {
			writeInput(t, root, "entry/assets/public/placeholder", "asset")
			for _, name := range []string{"app.wasm", "wasm_exec.js", "boot.js"} {
				writeInput(t, root, "entry/assets/_nextgo/"+runtimeID+"/"+name, "inert native fixture")
			}
			plan := &zoneReleasePlan{selected: ZoneManifest{ID: "site", Host: "site.test"}, spec: spec}
			if err := plan.bindAssets(context.Background(), filepath.Join(root, "entry/assets"), runtimeID); err != nil {
				t.Fatal(err)
			}
			source = zoneReleaseServerSource(lifecycleProject(), plan)
			result.args = []string{"-backend-hosts", "site.test"}
			expected := plan.spec.Expectation()
			result.expectation = &expected
		} else {
			manifest := zonecontract.IngressManifest{Version: zonecontract.Version, TopologySHA256: spec.TopologySHA256, Zones: []zonecontract.IngressZone{{Binding: spec.Zone, ActiveRelease: spec.ReleaseID, Releases: []zonecontract.IngressRelease{{Artifact: zonecontract.ReleaseManifest{Release: spec, BinarySHA256: strings.Repeat("d", 64)}, Origin: upstream}}}}}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			source = zoneIngressServerSource(data)
			const cleanup = "defer func(){drain,cancel:="
			tailAt := strings.Index(source, cleanup)
			if tailAt < 0 {
				t.Fatal("missing generated ingress cleanup")
			}
			originalTail := source[tailAt:]
			const constructor = "app,err:=nextgo.NewZoneIngress("
			if strings.Count(source, constructor) != 1 {
				t.Fatal("unexpected ingress constructor")
			}
			source = strings.Replace(source, constructor, "nativeApp,err:=nextgo.NewZoneIngress(", 1)
			const constructed = "TrustedTLSOffload:*trusted});if err!=nil{return err}\n"
			if strings.Count(source, constructed) != 1 {
				t.Fatal("unexpected ingress constructor end")
			}
			source = strings.Replace(source, constructed, constructed+"app:=&deadlineObservedIngress{ZoneIngress:nativeApp,held:make(chan context.Context,1)}\n", 1) + deadlineIngressObserver
			observedTailAt := strings.Index(source, cleanup)
			if observedTailAt < 0 || observedTailAt+len(originalTail) > len(source) || source[observedTailAt:observedTailAt+len(originalTail)] != originalTail {
				t.Fatal("observer changed emitted HTTP or application lifecycle tail")
			}
		}
	default:
		t.Fatal("unknown native lifecycle fixture", family)
	}
	if err := os.MkdirAll(filepath.Join(root, "entry"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeGo(filepath.Join(root, "entry/main.go"), source); err != nil {
		t.Fatal(err)
	}
	result.binary = filepath.Join(root, "server")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", result.binary, "./entry")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s deadline fixture: %v\n%s", family, err, output)
	}
	return result
}

func TestGeneratedHTTPDeadlineStillAcknowledgesApplicationCleanup(t *testing.T) {
	for _, family := range []string{"ordinary", "aggregate", "release", "ingress"} {
		t.Run(family, func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "deadline-record")
			entered, canceled := make(chan struct{}), make(chan struct{})
			var enterOnce, cancelOnce sync.Once
			var controlErrors lifecycleCapture
			appendRecord := func(line string) {
				f, err := os.OpenFile(record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					fmt.Fprintln(&controlErrors, err)
					return
				}
				if _, err := fmt.Fprintln(f, line); err != nil {
					fmt.Fprintln(&controlErrors, err)
				}
				if err := f.Close(); err != nil {
					fmt.Fprintln(&controlErrors, err)
				}
			}
			upstreamExpectation := zonecontract.Expectation{ZoneID: "site", ReleaseID: "deadline", TopologySHA256: strings.Repeat("c", 64)}
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/entered":
					enterOnce.Do(func() { close(entered) })
					w.WriteHeader(http.StatusNoContent)
				case "/canceled":
					cancelOnce.Do(func() { close(canceled) })
					w.WriteHeader(http.StatusNoContent)
				case "/cleanup":
					select {
					case <-canceled:
						w.WriteHeader(http.StatusNoContent)
					case <-r.Context().Done():
					}
				case "/ready":
					zonecontract.SetExpectation(w.Header(), upstreamExpectation)
					w.WriteHeader(http.StatusNoContent)
				case "/hold":
					zonecontract.SetExpectation(w.Header(), upstreamExpectation)
					enterOnce.Do(func() { close(entered) })
					<-r.Context().Done()
					appendRecord("request-canceled")
					cancelOnce.Do(func() { close(canceled) })
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(control.Close)
			fixture := buildDeadlineNativeFixture(t, family, control.URL)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			cmd := exec.CommandContext(ctx, fixture.binary, append([]string{"-addr", address}, fixture.args...)...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_LAMBDA_RESPONSE_MODE=", "NEXTGO_ENV_DIR=", "NEXTGO_ZONE_HOSTS=", "NEXTGO_ZONE_BACKEND_HOSTS=", "NEXTGO_TEST_DEADLINE_RECORD="+record, "NEXTGO_TEST_DEADLINE_CONTROL="+control.URL)
			var logs lifecycleCapture
			cmd.Stdout, cmd.Stderr = &logs, &logs
			if err := cmd.Start(); err != nil {
				cancel()
				t.Fatal(err)
			}
			done := make(chan struct{})
			var processErr error
			go func() { processErr = cmd.Wait(); close(done) }()
			client := &http.Client{Transport: &http.Transport{}, Timeout: 9 * time.Second}
			requestCtx, cancelRequest := context.WithCancel(context.Background())
			requestDone := make(chan struct{})
			requestStarted := false
			var requestErr error
			t.Cleanup(func() {
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
			request := func(ctx context.Context, path string) (*http.Response, error) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
				if err != nil {
					return nil, err
				}
				req.Host = "site.test"
				if fixture.expectation != nil {
					zonecontract.SetExpectation(req.Header, *fixture.expectation)
				}
				return client.Do(req)
			}
			readyCtx, stopReady := context.WithTimeout(context.Background(), 3*time.Second)
			defer stopReady()
			for {
				response, err := request(readyCtx, "/ready")
				if err == nil {
					response.Body.Close()
					if response.StatusCode == http.StatusNoContent {
						break
					}
				}
				select {
				case <-done:
					t.Fatalf("server exited before readiness: %v\n%s", processErr, logs.String())
				case <-readyCtx.Done():
					t.Fatalf("readiness timeout\n%s", logs.String())
				case <-time.After(10 * time.Millisecond):
				}
			}
			requestStarted = true
			go func() {
				defer close(requestDone)
				response, err := request(requestCtx, "/hold")
				requestErr = err
				if err == nil {
					_, requestErr = io.Copy(io.Discard, io.LimitReader(response.Body, 256))
					response.Body.Close()
				}
			}()
			select {
			case <-entered:
			case <-requestDone:
				t.Fatalf("held request ended early: %v\n%s", requestErr, logs.String())
			case <-readyCtx.Done():
				t.Fatalf("held request did not enter\n%s", logs.String())
			}
			started := time.Now()
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			// The actual unchanged HTTP grace is five seconds per case. The
			// eight-second observation bound also covers fresh cleanup and joins.
			drainCtx, stopDrain := context.WithTimeout(context.Background(), 8*time.Second)
			defer stopDrain()
			select {
			case <-done:
				if processErr != nil {
					t.Fatalf("shutdown exit: %v\n%s", processErr, logs.String())
				}
			case <-drainCtx.Done():
				t.Fatalf("deadline shutdown did not finish\n%s", logs.String())
			}
			select {
			case <-requestDone:
			case <-drainCtx.Done():
				t.Fatal("held client was not joined")
			}
			if requestErr == nil {
				t.Fatal("forced HTTP close did not interrupt held response")
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("missing cleanup record: %v\n%s", err, logs.String())
			}
			counts := map[string]int{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				counts[line]++
			}
			expectedKinds := 3
			if family == "ingress" {
				expectedKinds = 4
				if counts["downstream-canceled-before-close"] != 1 {
					t.Fatalf("HTTP Server.Close must cancel the downstream request before actual ingress Close: %q\n%s", data, logs.String())
				}
			}
			if len(counts) != expectedKinds || counts["close-context:<nil>"] != 1 || counts["close-ack"] != 1 || counts["request-canceled"] != 1 {
				t.Fatalf("cleanup requires a live independent context and one acknowledgment: %q\n%s", data, logs.String())
			}
			if controlErrors.String() != "" {
				t.Fatal(controlErrors.String())
			}
			t.Logf("%s native HTTP grace and cleanup acknowledged in %s", family, time.Since(started))
		})
	}
}
