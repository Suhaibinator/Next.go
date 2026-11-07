package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const exportPhaseHelperEnv = "NEXTGO_TEST_EXPORT_PHASE_HELPER"
const exportPhaseHelperMarker = "static-export-child-v1"

// Ordinary package tests still use m.Run. Only the explicitly marked child
// serves, using the same arguments supplied by the real exporter startup path.
func TestMain(m *testing.M) {
	listener, helper, err := prepareExportPhaseHelper(os.Args[1:], os.Environ(), net.Listen)
	if !helper {
		os.Exit(m.Run())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(32)
	}
	if err := serveExportPhaseHelper(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(33)
	}
	os.Exit(0)
}

func exportPhaseHelperRequested(env []string) (bool, error) {
	seen := false
	for _, entry := range env {
		key, value, present := strings.Cut(entry, "=")
		if key != exportPhaseHelperEnv {
			continue
		}
		if seen || !present || value != exportPhaseHelperMarker {
			return true, errors.New("invalid or duplicate static-export helper marker")
		}
		seen = true
	}
	return seen, nil
}

func exportPhaseHelperEnvironment(env []string) []string {
	child := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != exportPhaseHelperEnv {
			child = append(child, entry)
		}
	}
	return append(child, exportPhaseHelperEnv+"="+exportPhaseHelperMarker)
}

// This private fixture accepts the exporter's exact argument protocol, rather
// than registering application flags on the package test runner's FlagSet.
func exportPhaseHelperAddress(args []string) (string, error) {
	if len(args) != 3 || args[0] != "-static-export" || args[1] != "-addr" {
		return "", errors.New("static-export helper requires exact purpose and address arguments")
	}
	host, port, err := net.SplitHostPort(args[2])
	if err != nil || host != "127.0.0.1" {
		return "", errors.New("static-export helper requires a canonical loopback address")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return "", errors.New("static-export helper requires a canonical nonzero port")
	}
	return args[2], nil
}

func prepareExportPhaseHelper(args, env []string, listen func(string, string) (net.Listener, error)) (net.Listener, bool, error) {
	helper, err := exportPhaseHelperRequested(env)
	if err != nil || !helper {
		return nil, helper, err
	}
	addr, err := exportPhaseHelperAddress(args)
	if err != nil {
		return nil, true, err
	}
	listener, err := listen("tcp", addr)
	if err != nil {
		return nil, true, fmt.Errorf("bind static-export helper: %w", err)
	}
	return listener, true, nil
}

func exportPhaseHelperHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fixture-Phase", "static-export")
		_, _ = w.Write([]byte("ready"))
	})
	return mux
}

func serveExportPhaseHelper(listener net.Listener) error {
	defer listener.Close()
	return http.Serve(listener, exportPhaseHelperHandler())
}

// Test the real exporter child startup purpose without a nested compiler job.
func TestExportServingChildUsesStaticPurpose(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, stop, err := startExportServerEnv(ctx, binary, exportPhaseHelperEnvironment(os.Environ()))
	if err != nil {
		t.Fatalf("start static-export fixture: %v (context: %v)", err, ctx.Err())
	}
	defer stop()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(base)
	if err != nil {
		t.Fatalf("request static-export fixture: %v (context: %v)", err, ctx.Err())
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("X-Fixture-Phase") != "static-export" {
		t.Fatal(response.StatusCode, response.Header)
	}
}

func TestExportPhaseHelperEnvironmentGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    []string
		helper bool
		bad    bool
	}{
		{"ordinary", []string{"OTHER=1"}, false, false},
		{"exact", []string{exportPhaseHelperEnv + "=" + exportPhaseHelperMarker}, true, false},
		{"missing-value-separator", []string{exportPhaseHelperEnv}, true, true},
		{"empty", []string{exportPhaseHelperEnv + "="}, true, true},
		{"wrong", []string{exportPhaseHelperEnv + "=1"}, true, true},
		{"suffix", []string{exportPhaseHelperEnv + "=" + exportPhaseHelperMarker + "x"}, true, true},
		{"duplicate", []string{exportPhaseHelperEnv + "=" + exportPhaseHelperMarker, exportPhaseHelperEnv + "=" + exportPhaseHelperMarker}, true, true},
		{"conflicting", []string{exportPhaseHelperEnv + "=" + exportPhaseHelperMarker, exportPhaseHelperEnv + "=wrong"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper, err := exportPhaseHelperRequested(tc.env)
			if helper != tc.helper || (err != nil) != tc.bad {
				t.Fatal(helper, err)
			}
		})
	}
}

func TestExportPhaseHelperChildEnvironmentIsOwnedCopy(t *testing.T) {
	env := []string{"OTHER=keep", exportPhaseHelperEnv + "=old", exportPhaseHelperEnv}
	child := exportPhaseHelperEnvironment(env)
	if len(child) != 2 || child[0] != "OTHER=keep" || child[1] != exportPhaseHelperEnv+"="+exportPhaseHelperMarker {
		t.Fatal(child)
	}
	child[0] = "OTHER=changed"
	if env[0] != "OTHER=keep" || env[1] != exportPhaseHelperEnv+"=old" || env[2] != exportPhaseHelperEnv {
		t.Fatal("caller environment changed", env)
	}
	if helper, err := exportPhaseHelperRequested(child); !helper || err != nil {
		t.Fatal(helper, err)
	}
}

func TestExportPhaseHelperArgumentsAreExact(t *testing.T) {
	if addr, err := exportPhaseHelperAddress([]string{"-static-export", "-addr", "127.0.0.1:4185"}); err != nil || addr != "127.0.0.1:4185" {
		t.Fatal(addr, err)
	}
	for _, args := range [][]string{
		nil,
		{"-addr", "127.0.0.1:4185"},
		{"-static-export"},
		{"-static-export", "-addr"},
		{"-static-export=false", "-addr", "127.0.0.1:4185"},
		{"-unknown", "-addr", "127.0.0.1:4185"},
		{"-static-export", "-addr", "127.0.0.1:4185", "positional"},
		{"-static-export", "-static-export", "-addr", "127.0.0.1:4185"},
		{"-static-export", "-addr", "127.0.0.1:4185", "-addr", "127.0.0.1:4186"},
		{"-addr", "127.0.0.1:4185", "-static-export"},
		{"-static-export", "-addr", "localhost:4185"},
		{"-static-export", "-addr", "0.0.0.0:4185"},
		{"-static-export", "-addr", "[::1]:4185"},
		{"-static-export", "-addr", "127.0.0.1:0"},
		{"-static-export", "-addr", "127.0.0.1:65536"},
		{"-static-export", "-addr", "127.0.0.1:+4185"},
		{"-static-export", "-addr", "127.0.0.1:04185"},
		{"-static-export", "-addr", "127.0.0.1:"},
	} {
		if _, err := exportPhaseHelperAddress(args); err == nil {
			t.Fatal("accepted malformed helper arguments", args)
		}
	}
}

func TestExportPhaseHelperRejectsBeforeListening(t *testing.T) {
	calls := 0
	listen := func(string, string) (net.Listener, error) {
		calls++
		return nil, errors.New("unexpected listener invocation")
	}
	listener, helper, err := prepareExportPhaseHelper([]string{"-test.run=anything"}, []string{"OTHER=1"}, listen)
	if listener != nil || helper || err != nil || calls != 0 {
		t.Fatal("ordinary test invocation intercepted", listener, helper, err, calls)
	}
	for _, tc := range []struct{ args, env []string }{
		{[]string{"-static-export", "-addr", "127.0.0.1:4185"}, []string{exportPhaseHelperEnv + "=wrong"}},
		{[]string{"-unknown"}, []string{exportPhaseHelperEnv + "=" + exportPhaseHelperMarker}},
	} {
		listener, helper, err := prepareExportPhaseHelper(tc.args, tc.env, listen)
		if listener != nil || !helper || err == nil || calls != 0 {
			t.Fatal("malformed helper reached listener", listener, helper, err, calls)
		}
	}
}

func TestExportPhaseHelperBindFailureIsReturned(t *testing.T) {
	owner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	listener, helper, err := prepareExportPhaseHelper([]string{"-static-export", "-addr", owner.Addr().String()}, []string{exportPhaseHelperEnv + "=" + exportPhaseHelperMarker}, net.Listen)
	var operation *net.OpError
	if listener != nil || !helper || !errors.As(err, &operation) || operation.Op != "listen" {
		t.Fatal("occupied address did not fail binding", listener, helper, err)
	}
}

type exportPhaseFailedListener struct {
	failure error
	closed  bool
}

func (l *exportPhaseFailedListener) Accept() (net.Conn, error) { return nil, l.failure }
func (l *exportPhaseFailedListener) Close() error             { l.closed = true; return nil }
func (l *exportPhaseFailedListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

func TestExportPhaseHelperListenerFailureIsReturned(t *testing.T) {
	want := errors.New("fixture listener failed")
	listener := &exportPhaseFailedListener{failure: want}
	if err := serveExportPhaseHelper(listener); !errors.Is(err, want) || !listener.closed {
		t.Fatal("listener failure or cleanup lost", err, listener.closed)
	}
}

func TestExportPhaseHelperResponseContract(t *testing.T) {
	response := httptest.NewRecorder()
	exportPhaseHelperHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil))
	if response.Code != 200 || response.Header().Get("X-Fixture-Phase") != "static-export" || response.Body.String() != "ready" {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
}
