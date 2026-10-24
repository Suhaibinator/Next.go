//go:build deployment

package deployment

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

const durableCacheSecret = "nextgo-durable-cache-public-conformance-token"

var durablePolicy = shared.Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"durable"}, Paths: []string{"/durable"}}

func durableDirectory(t *testing.T) string {
	t.Helper()
	var dir string
	if base := os.Getenv("NEXTGO_TEST_CACHE_TMPDIR"); base != "" {
		var err error
		dir, err = os.MkdirTemp(base, "nextgo-durable-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	} else {
		dir = t.TempDir()
	}
	// Go versions differ in TempDir's mode. The actual backend deliberately
	// rejects group/world-readable directories instead of silently widening trust.
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func launchDurableCache(t *testing.T, dir, addr string) *process {
	t.Helper()
	binary := os.Getenv("NEXTGO_TEST_CACHE_BINARY")
	if binary == "" {
		t.Skip("set NEXTGO_TEST_CACHE_BINARY to the built nextgo-cache executable")
	}
	if expected := os.Getenv("NEXTGO_TEST_UID"); expected != "" && expected != strconv.Itoa(os.Getuid()) {
		t.Fatalf("expected UID %s, got %d", expected, os.Getuid())
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-addr", addr, "-namespace", "demo", "-entries", "16", "-bytes", "1048576", "-data-dir", dir, "-disk-bytes", "8388608")
	cmd.Dir = "/"
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "NEXTGO_CACHE_") && !strings.HasPrefix(key, "AWS_LAMBDA_") && !strings.HasPrefix(key, "NEXTGO_LAMBDA_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "NEXTGO_CACHE_TOKEN="+durableCacheSecret)
	p := &process{cmd: cmd, done: make(chan struct{}), logs: &capture{}}
	cmd.Stdout, cmd.Stderr = p.logs, p.logs
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

func durableHTTP(t *testing.T, addr string) *shared.HTTP {
	t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	backend, err := shared.NewHTTP("http://"+addr, durableCacheSecret, &http.Client{Timeout: 2 * time.Second, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func openDurableCache(t *testing.T, dir string) (*process, *shared.HTTP) {
	t.Helper()
	return openDurableCacheAt(t, dir, address(t))
}
func openDurableCacheAt(t *testing.T, dir, addr string) (*process, *shared.HTTP) {
	t.Helper()
	p := launchDurableCache(t, dir, addr)
	backend := durableHTTP(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		view, err := backend.Begin(ctx, shared.ID{Namespace: "demo", Schema: "durable-tests-v1", Key: "readiness"}, durablePolicy, time.Minute)
		if err == nil {
			if view.Lease != "" {
				if err := backend.Release(ctx, shared.ID{Namespace: "demo", Schema: "durable-tests-v1", Key: "readiness"}, view.Lease); err != nil {
					t.Fatal(err)
				}
			}
			return p, backend
		}
		select {
		case <-p.done:
			t.Fatalf("durable service startup: %v\n%s", p.err, p.logs.String())
		case <-ctx.Done():
			t.Fatalf("durable service readiness timeout: %v\n%s", err, p.logs.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestDurableCacheRenderedPageSurvivesAuthorityRestart(t *testing.T) {
	const host = "durable-pages.test"
	dir, cacheAddr, appAddr := durableDirectory(t), address(t), address(t)
	authority, _ := openDurableCacheAt(t, dir, cacheAddr)
	app := start(t, []string{"NEXTGO_DEMO_SHARED_ENDPOINT=http://" + cacheAddr, "NEXTGO_CACHE_TOKEN=" + durableCacheSecret}, "-addr", appAddr)
	origin := "http://" + appAddr
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	read := func() ([]byte, string, http.Header) {
		data, headers := sharedPageRequest(t, client, app, origin, host, "/shared-pages/alpha", nil, 200)
		if bytes.Contains(data, []byte(durableCacheSecret)) {
			t.Fatal("cache credential rendered in page")
		}
		return data, mustSharedPage(t, data, "alpha"), headers
	}
	first, id, headers := read()
	if headers.Get("X-Nextgo-Cache") != "miss" {
		t.Fatal(headers)
	}
	killDurableCache(t, authority)
	restarted, recovered := openDurableCacheAt(t, dir, cacheAddr)
	second, same, headers := read()
	if same != id || !bytes.Equal(first, second) || headers.Get("X-Nextgo-Cache") != "fresh" {
		t.Fatal("app did not recover its complete frozen page from the durable authority", id, same, headers)
	}
	// Invalidate only the remote authority: a hidden process-local page cache
	// must not satisfy this read. Kill again after ACK, before the app rereads.
	if n, err := recovered.Expire(context.Background(), "demo", shared.Selector{Kind: "prefix", Value: "/shared-pages/alpha"}); err != nil || n != 1 {
		t.Fatal("page invalidation not durably acknowledged", n, err)
	}
	killDurableCache(t, restarted)
	final, _ := openDurableCacheAt(t, dir, cacheAddr)
	_, next, headers := read()
	if next == id || headers.Get("X-Nextgo-Cache") != "miss" {
		t.Fatal("acknowledged page invalidation resurrected a snapshot", id, next, headers)
	}
	app.stop(t)
	final.stop(t)
}

func durableID(key string) shared.ID {
	return shared.ID{Namespace: "demo", Schema: "durable-tests-v1", Key: key}
}

func durablePublish(t *testing.T, backend shared.Backend, id shared.ID, policy shared.Policy, value []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, err := backend.Begin(ctx, id, policy, time.Minute)
	if err != nil || lease.Lease == "" || lease.Busy {
		t.Fatalf("acquire durable publication: %+v %v", lease, err)
	}
	view, err := backend.Commit(ctx, id, lease.Lease, value)
	if err != nil || !view.Found || !bytes.Equal(view.Value, value) {
		t.Fatalf("acknowledged durable publication: %+v %v", view, err)
	}
}

func killDurableCache(t *testing.T, p *process) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err == nil {
			t.Fatal("forced cache termination unexpectedly exited cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forced cache process did not exit")
	}
}

func requireCacheStartupFailure(t *testing.T, p *process, addr string, privateValue string) {
	t.Helper()
	select {
	case <-p.done:
		if p.err == nil {
			t.Fatal("invalid/locked durable store started successfully", p.logs.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invalid/locked durable store did not refuse startup", p.logs.String())
	}
	if strings.Contains(p.logs.String(), durableCacheSecret) || privateValue != "" && strings.Contains(p.logs.String(), privateValue) {
		t.Fatal("durable startup diagnostics disclosed credentials or persisted value")
	}
	connection, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("failed durable startup left a listener")
	}
}

func TestDurableCacheAcknowledgmentsSurviveProcessKill(t *testing.T) {
	dir := durableDirectory(t)
	p, backend := openDurableCache(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	kept, expired, stale, pending := durableID("kept"), durableID("expired"), durableID("stale"), durableID("pending")
	value := []byte{0, 255, 1, 'd', 'u', 'r', 'a', 'b', 'l', 'e'}
	durablePublish(t, backend, kept, durablePolicy, value)
	durablePublish(t, backend, expired, durablePolicy, []byte("deleted-before-crash"))
	durablePublish(t, backend, stale, durablePolicy, []byte("last-good"))
	if n, err := backend.Expire(ctx, "demo", shared.Selector{Kind: "key", Value: expired.Key}); err != nil || n != 1 {
		t.Fatal("invalidation was not acknowledged", n, err)
	}
	if n, err := backend.MarkStale(ctx, "demo", shared.Selector{Kind: "key", Value: stale.Key}, shared.Never); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	refresh, err := backend.BeginStale(ctx, stale, durablePolicy, time.Minute)
	if err != nil || !refresh.Found || refresh.Lease == "" {
		t.Fatal(refresh, err)
	}
	lease, err := backend.Begin(ctx, pending, durablePolicy, time.Minute)
	if err != nil || lease.Lease == "" {
		t.Fatal(lease, err)
	}
	// SIGKILL cannot flush a deferred close. Every successful response above
	// must already have synchronized its published value/invalidation to disk.
	killDurableCache(t, p)
	restarted, recovered := openDurableCache(t, dir)
	for _, old := range []struct {
		id    shared.ID
		token string
	}{{stale, refresh.Lease}, {pending, lease.Lease}} {
		view, err := recovered.Commit(ctx, old.id, old.token, []byte("obsolete-owner"))
		if err != nil || view.Found {
			t.Fatal("pre-restart lease published before any replacement acquired ownership", view, err)
		}
	}
	view, err := recovered.Begin(ctx, kept, durablePolicy, time.Minute)
	if err != nil || !view.Found || !bytes.Equal(view.Value, value) || view.Lease != "" {
		t.Fatal("acknowledged binary value lost/corrupted", view, err)
	}
	missing, err := recovered.Begin(ctx, expired, durablePolicy, time.Minute)
	if err != nil || missing.Found || missing.Lease == "" {
		t.Fatal("acknowledged invalidation resurrected old data", missing, err)
	}
	if err := recovered.Release(ctx, expired, missing.Lease); err != nil {
		t.Fatal(err)
	}
	lastGood, err := recovered.BeginStale(ctx, stale, durablePolicy, time.Minute)
	if err != nil || !lastGood.Found || string(lastGood.Value) != "last-good" || lastGood.FreshFor != 0 || lastGood.Lease == "" {
		t.Fatal("stale publication lost/reset across restart", lastGood, err)
	}
	if err := recovered.Release(ctx, stale, lastGood.Lease); err != nil {
		t.Fatal(err)
	}
	restarted.stop(t)
}

func TestDurableCacheStaleDeadlineAndAgeSurviveRestart(t *testing.T) {
	dir := durableDirectory(t)
	p, backend := openDurableCache(t, dir)
	id := durableID("clipped-never")
	policy := durablePolicy
	policy.ExpireAfter = shared.Never
	durablePublish(t, backend, id, policy, []byte("finite-last-good"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if n, err := backend.MarkStale(ctx, "demo", shared.Selector{Kind: "key", Value: id.Key}, 2*time.Second); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	before, err := backend.BeginStale(ctx, id, policy, time.Minute)
	selectedAt := time.Now()
	if err != nil || !before.Found || before.NoExpiry || before.FreshFor != 0 || before.ExpiresIn <= 0 || before.ExpiresIn > 2*time.Second {
		t.Fatal(before, err)
	}
	killDurableCache(t, p)
	// Deliberate downtime makes a reset deadline/age observable independently
	// of ordinary loopback transport jitter. The real wall clock keeps advancing.
	time.Sleep(300 * time.Millisecond)
	restarted, recovered := openDurableCache(t, dir)
	after, err := recovered.BeginStale(ctx, id, policy, time.Minute)
	elapsed := time.Since(selectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if after.Found {
		if after.NoExpiry || after.FreshFor != 0 || after.ExpiresIn > before.ExpiresIn-elapsed+100*time.Millisecond || after.Age < before.Age+200*time.Millisecond {
			t.Fatal("restart extended/reset the original generation", before, after, elapsed)
		}
	} else if elapsed+100*time.Millisecond < before.ExpiresIn {
		t.Fatal("restart discarded an unexpired last-good generation", after, elapsed)
	}
	if after.Lease != "" {
		if err := recovered.Release(ctx, id, after.Lease); err != nil {
			t.Fatal(err)
		}
	}
	if remaining := time.Until(selectedAt.Add(before.ExpiresIn + 150*time.Millisecond)); remaining > 0 {
		time.Sleep(remaining)
	}
	expired, err := recovered.BeginStale(ctx, id, policy, time.Minute)
	if err != nil || expired.Found {
		t.Fatal("original hard deadline no longer enforced after restart", expired, err)
	}
	if expired.Lease != "" {
		if err := recovered.Release(ctx, id, expired.Lease); err != nil {
			t.Fatal(err)
		}
	}
	restarted.stop(t)
}

func TestDurableCacheRejectsConcurrentProcessWriter(t *testing.T) {
	dir := durableDirectory(t)
	p, backend := openDurableCache(t, dir)
	id := durableID("writer-lock")
	durablePublish(t, backend, id, durablePolicy, []byte("sole-owner"))
	addr := address(t)
	second := launchDurableCache(t, dir, addr)
	requireCacheStartupFailure(t, second, addr, "sole-owner")
	view, err := backend.Begin(context.Background(), id, durablePolicy, time.Minute)
	if err != nil || !view.Found || string(view.Value) != "sole-owner" {
		t.Fatal("rejected writer disrupted the active authority", view, err)
	}
	p.stop(t)
}

func TestDurableCacheCorruptionAndMissingJournalFailClosed(t *testing.T) {
	for _, mode := range []string{"corrupt", "missing"} {
		t.Run(mode, func(t *testing.T) {
			dir := durableDirectory(t)
			p, backend := openDurableCache(t, dir)
			marker := "persisted-private-payload-must-not-appear-in-diagnostics"
			durablePublish(t, backend, durableID("corruption"), durablePolicy, []byte(marker))
			p.stop(t)
			journal := filepath.Join(dir, "cache.wal")
			original, err := os.ReadFile(journal)
			if err != nil || len(original) < 64 {
				t.Fatal("missing committed journal", len(original), err)
			}
			if mode == "missing" {
				if err := os.Remove(journal); err != nil {
					t.Fatal(err)
				}
			} else {
				corrupt := bytes.Clone(original)
				corrupt[len(corrupt)-1] ^= 0xff // same length, invalid committed record checksum
				if err := os.WriteFile(journal, corrupt, 0600); err != nil {
					t.Fatal(err)
				}
			}
			addr := address(t)
			failed := launchDurableCache(t, dir, addr)
			requireCacheStartupFailure(t, failed, addr, marker)
			// Restore exactly the last acknowledged bytes under operator control,
			// not an older automatic fallback. Failed Open must release its lock.
			if err := os.WriteFile(journal, original, 0600); err != nil {
				t.Fatal(err)
			}
			repaired, recovered := openDurableCache(t, dir)
			view, err := recovered.Begin(context.Background(), durableID("corruption"), durablePolicy, time.Minute)
			if err != nil || !view.Found || !bytes.Equal(view.Value, []byte(marker)) {
				t.Fatal("exact operator repair failed to reopen acknowledged data", view, err)
			}
			repaired.stop(t)
		})
	}
}
