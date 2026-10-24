package dev

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"testing"
	"time"
)

func TestChildHelper(t *testing.T) {
	if os.Getenv("NEXTGO_TEST_CHILD") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	os.Exit(0)
}

func TestChildStopIsRepeatableAfterExit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestChildHelper$")
	cmd.Env = append(os.Environ(), "NEXTGO_TEST_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	t.Cleanup(c.stop)
	started := time.Now()
	c.stop()
	c.stop()
	if time.Since(started) > 2*time.Second {
		t.Fatal("stopping an exited child must not wait for the kill timeout")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("child was not reaped")
	}
}

func TestExitedChildClearsProxyAndReportsFailure(t *testing.T) {
	var output bytes.Buffer
	updates := make(chan string, 1)
	s := &supervisor{authority: "127.0.0.1:3000", proxy: &httputil.ReverseProxy{}, listeners: map[chan string]bool{updates: true}, output: &output}
	c := &child{done: make(chan struct{}), err: errors.New("unexpected termination")}
	if s.exited(c) {
		t.Fatal("live child reported as exited")
	}
	close(c.done)
	if !s.exited(c) || s.proxy != nil || !strings.Contains(s.buildError, "unexpected termination") {
		t.Fatal("exited child left an active proxy or no error")
	}
	if !strings.Contains(<-updates, "event: build-error") {
		t.Fatal("browser did not receive a development error")
	}
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:3000/", nil))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "Saving a fix rebuilds automatically") {
		t.Fatalf("unexpected crashed-server response: %d %s", response.Code, response.Body.String())
	}
}

func TestInitialBuildErrorIsEscapedAndVisible(t *testing.T) {
	s := &supervisor{authority: "127.0.0.1:3000", buildError: "compile failed: <bad source>"}
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:3000/", nil))
	body := response.Body.String()
	if response.Code != 503 || !strings.Contains(body, "compile failed: &lt;bad source&gt;") || strings.Contains(body, "<bad source>") {
		t.Fatalf("initial compiler error was absent or unescaped: %d %s", response.Code, body)
	}
	if !strings.Contains(body, "events.addEventListener('build-error'") {
		t.Fatal("waiting page must display later build errors without requiring a reload")
	}
}

func TestEventsReplayCurrentBuildAndErrors(t *testing.T) {
	s := &supervisor{authority: "127.0.0.1:3000", id: "new", buildError: "compile failed", listeners: map[chan string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/_nextgo/events?build=old", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { s.events(response, request); close(done) }()
	// Wait for registration rather than sleeping or inspecting a concurrent recorder.
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		registered := len(s.listeners) == 1
		s.mu.Unlock()
		if registered {
			break
		}
		select {
		case <-deadline:
			t.Fatal("event stream was not registered")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream leaked after request cancellation")
	}
	if len(s.listeners) != 0 {
		t.Fatal("event listener leaked")
	}
	for _, expected := range []string{"event: reload", "event: build-error", "compile failed"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("missing %q from %q", expected, response.Body.String())
		}
	}
}

func TestHostValidationProtectsEveryDevelopmentSurface(t *testing.T) {
	s := &supervisor{authority: "127.0.0.1:3000"}
	for _, path := range []string{"/", "/_nextgo/events", "/_nextgo/old/app.wasm", "/_nextgo/actions/increment"} {
		for _, host := range []string{"attacker.example:3000", "127.0.0.1:4000", "localhost", "localhost:03000", "localhost:3000.attacker.example", "user@localhost:3000", "[::]:3000", ""} {
			r := httptest.NewRequest("GET", "http://127.0.0.1:3000"+path, nil)
			r.Host = host
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 403 || strings.Contains(w.Body.String(), "Building") {
				t.Fatalf("%s host %q: %d %s", path, host, w.Code, w.Body.String())
			}
		}
	}
	for _, host := range []string{"127.0.0.1:3000", "127.0.0.2:3000", "localhost:3000", "LOCALHOST:3000", "[::1]:3000"} {
		if !s.allowedHost(host) {
			t.Fatalf("rejected local host %q", host)
		}
	}
	explicit := &supervisor{authority: "192.168.1.2:3000"}
	if !explicit.allowedHost("192.168.1.2:3000") || explicit.allowedHost("192.168.1.3:3000") {
		t.Fatal("explicit interface host policy")
	}
	wildcard := &supervisor{authority: "[::]:3000"}
	if wildcard.allowedHost("remote.example:3000") || !wildcard.allowedHost("localhost:3000") {
		t.Fatal("wildcard must retain local hostname policy")
	}
}
