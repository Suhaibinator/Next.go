package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

const commandTestToken = "nextgo-cache-cli-test-credential-32-bytes"

type commandStatus struct {
	startupWriter
	mu     sync.Mutex
	output strings.Builder
}

func (s *commandStatus) Write(p []byte) (int, error) {
	s.mu.Lock()
	_, _ = s.output.Write(p)
	s.mu.Unlock()
	return s.startupWriter.Write(p)
}
func (s *commandStatus) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.output.String() }

type cacheCommand struct {
	backend  *shared.HTTP
	endpoint string
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
	stopOnce sync.Once
	status   *commandStatus
}

func startCacheCommand(t *testing.T, args ...string) *cacheCommand {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	address := make(chan string, 1)
	run := &cacheCommand{cancel: cancel, done: make(chan struct{}), status: &commandStatus{startupWriter: startupWriter{address}}}
	all := append([]string{"-addr", "127.0.0.1:0", "-namespace", "app"}, args...)
	go func() { run.err = execute(ctx, all, commandTestToken, run.status); close(run.done) }()
	select {
	case addr := <-address:
		run.endpoint = "http://" + addr
	case <-run.done:
		cancel()
		t.Fatal("cache command startup failed", run.err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("cache command startup timeout")
	}
	t.Cleanup(func() { run.stop(t) })
	var err error
	run.backend, err = shared.NewHTTP(run.endpoint, commandTestToken, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return run
}
func (r *cacheCommand) stop(t *testing.T) {
	t.Helper()
	r.stopOnce.Do(func() {
		r.cancel()
		select {
		case <-r.done:
			if r.err != nil {
				t.Error("cache command shutdown", r.err)
			}
		case <-time.After(7 * time.Second):
			t.Error("cache command shutdown timeout")
		}
	})
}
func durableDirectory(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "cache") }
func commandPublish(t *testing.T, b *shared.HTTP, id shared.ID, p shared.Policy, value string) {
	t.Helper()
	v, err := b.Begin(context.Background(), id, p, time.Second)
	if err != nil || v.Lease == "" {
		t.Fatal(v, err)
	}
	v, err = b.Commit(context.Background(), id, v.Lease, []byte(value))
	if err != nil || !v.Found || string(v.Value) != value {
		t.Fatal(v, err)
	}
}

func TestCommandDurableRestartKeepsValuesAgeAndExpiration(t *testing.T) {
	dir := durableDirectory(t)
	args := []string{"-data-dir", dir, "-entries", "8", "-bytes", "1048576", "-disk-bytes", "65536"}
	first := startCacheCommand(t, args...)
	if !strings.Contains(first.status.String(), "durable file storage") || strings.Contains(first.status.String(), commandTestToken) {
		t.Fatal(first.status.String())
	}
	p := shared.Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"public"}, Paths: []string{"/posts/a"}}
	keep := shared.ID{Namespace: "app", Schema: "v1", Key: "keep"}
	expired := keep
	expired.Key = "expired"
	pending := keep
	pending.Key = "pending"
	commandPublish(t, first.backend, keep, p, "retained")
	commandPublish(t, first.backend, expired, p, "obsolete")
	if count, err := first.backend.Expire(context.Background(), "app", shared.Selector{Kind: "key", Value: "expired"}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	lease, err := first.backend.Begin(context.Background(), pending, p, time.Second)
	if err != nil || lease.Lease == "" {
		t.Fatal(lease, err)
	}
	time.Sleep(25 * time.Millisecond)
	first.stop(t)
	second := startCacheCommand(t, args...)
	v, err := second.backend.Begin(context.Background(), keep, p, time.Second)
	if err != nil || !v.Found || string(v.Value) != "retained" || v.Age < 25*time.Millisecond || v.FreshFor >= time.Hour {
		t.Fatal("restart changed value or reset generation age", v, err)
	}
	v, err = second.backend.Begin(context.Background(), expired, p, time.Second)
	if err != nil || v.Found || v.Lease == "" {
		t.Fatal("acknowledged expiration resurrected", v, err)
	}
	if err := second.backend.Release(context.Background(), expired, v.Lease); err != nil {
		t.Fatal(err)
	}
	v, err = second.backend.Commit(context.Background(), pending, lease.Lease, []byte("late old process"))
	if err != nil || v.Found {
		t.Fatal("old process lease survived restart", v, err)
	}
	second.stop(t)
}

func TestCommandDurableFlagsAndDefaultMemory(t *testing.T) {
	for _, args := range [][]string{{"-disk-bytes", "1"}, {"-disk-bytes", "-1"}, {"-data-dir", durableDirectory(t), "-disk-bytes", "4095"}, {"-data-dir", durableDirectory(t), "-entries", "0"}, {"-data-dir", durableDirectory(t), "-bytes", "0"}} {
		if err := execute(context.Background(), args, commandTestToken, io.Discard); err == nil {
			t.Fatal("invalid durable flags accepted", args)
		}
	}
	for _, args := range [][]string{nil, {"-disk-bytes", "0"}} {
		run := startCacheCommand(t, args...)
		if !strings.Contains(run.status.String(), "memory only") || strings.Contains(run.status.String(), "durable") {
			t.Fatal(run.status.String())
		}
		run.stop(t)
	}
}

func TestCommandAppliesDurableDiskBudget(t *testing.T) {
	run := startCacheCommand(t, "-data-dir", durableDirectory(t), "-bytes", "1048576", "-disk-bytes", "8192")
	id := shared.ID{Namespace: "app", Schema: "v1", Key: "disk-budget"}
	p := shared.Policy{ExpireAfter: time.Hour}
	commandPublish(t, run.backend, id, p, "last good")
	lease, err := run.backend.Begin(context.Background(), id, p, time.Second)
	if err != nil || lease.Lease == "" {
		t.Fatal(lease, err)
	}
	if _, err := run.backend.Commit(context.Background(), id, lease.Lease, []byte(strings.Repeat("x", 16000))); !errors.Is(err, shared.ErrCapacity) {
		t.Fatal("disk quota was ignored", err)
	}
	if err := run.backend.Release(context.Background(), id, lease.Lease); err != nil {
		t.Fatal(err)
	}
	view, err := run.backend.Begin(context.Background(), id, p, time.Second)
	if err != nil || !view.Found || string(view.Value) != "last good" {
		t.Fatal("rejected disk write changed the last good value", view, err)
	}
	if err := run.backend.Release(context.Background(), id, view.Lease); err != nil {
		t.Fatal(err)
	}
}

type failedStatusWriter struct{ err error }

func (w failedStatusWriter) Write([]byte) (int, error) { return 0, w.err }
func TestCommandStartupFailuresReleaseDurableOwnership(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	statusError := errors.New("status pipe closed")
	for _, tc := range []struct {
		name  string
		args  []string
		token string
		out   io.Writer
		want  error
	}{
		{name: "credential", token: "", out: io.Discard},
		{name: "namespace", args: []string{"-namespace", "bad/name"}, token: commandTestToken, out: io.Discard},
		{name: "TLS", args: []string{"-tls-cert", "missing.pem", "-tls-key", "missing-key.pem"}, token: commandTestToken, out: io.Discard},
		{name: "listener", args: []string{"-addr", busy.Addr().String()}, token: commandTestToken, out: io.Discard},
		{name: "status-output", token: commandTestToken, out: failedStatusWriter{statusError}, want: statusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := durableDirectory(t)
			args := append([]string{"-addr", "127.0.0.1:0", "-data-dir", dir}, tc.args...)
			err := execute(context.Background(), args, tc.token, tc.out)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal("startup error lost", err)
			}
			file, err := shared.OpenFile(dir, shared.FileOptions{})
			if err != nil {
				t.Fatal("failed startup retained durable ownership", err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommandDrainsRequestBeforeClosingDurableStorage(t *testing.T) {
	dir := durableDirectory(t)
	run := startCacheCommand(t, "-data-dir", dir)
	endpoint, err := url.Parse(run.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", endpoint.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nExpect: 100-continue\r\nTransfer-Encoding: chunked\r\n\r\n", endpoint.Host, commandTestToken)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	interim, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	interim.Body.Close()
	if interim.StatusCode != 100 {
		t.Fatal("request did not enter handler", interim.StatusCode)
	}
	run.cancel()
	select {
	case <-run.done:
		t.Fatal("service exited before its active request drained", run.err)
	case <-time.After(30 * time.Millisecond):
	}
	payload := `{"Version":3,"Operation":"begin","ID":{"Namespace":"app","Schema":"v1","Key":"drain"},"Policy":{"FreshFor":1000000000,"ExpireAfter":60000000000},"LeaseFor":1000000000}`
	if _, err := fmt.Fprintf(conn, "%x\r\n%s\r\n0\r\n\r\n", len(payload), payload); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("durable backend closed before admitted operation", response.StatusCode)
	}
	run.stop(t)
	file, err := shared.OpenFile(dir, shared.FileOptions{})
	if err != nil {
		t.Fatal("drained service retained ownership", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandCanceledDurableStartupDoesNotCreateStorage(t *testing.T) {
	dir := durableDirectory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execute(ctx, []string{"-data-dir", dir}, commandTestToken, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("canceled startup touched durable storage", err)
	}
}
