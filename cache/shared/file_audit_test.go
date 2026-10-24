package shared

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fileAuditDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "authority")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFileAuditRestartPreservesAgeAndDropsEveryLease(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	p := Policy{FreshFor: 0, ExpireAfter: time.Minute, Tags: []string{"posts"}, Paths: []string{"/posts/a"}}
	origin := time.Now().Add(-2 * time.Second)
	f.now = func() time.Time { return origin }
	first := acquire(t, f, testID, p)
	publish(t, f, testID, first.Lease, []byte("last good"))
	pending := acquire(t, f, testID, p)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cache.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(first.Lease)) || bytes.Contains(data, []byte(pending.Lease)) {
		t.Fatal("ephemeral ownership tokens reached durable storage")
	}
	reopened := openFileTest(t, dir, FileOptions{})
	next := acquire(t, reopened, testID, p)
	if !next.Found || string(next.Value) != "last good" || next.Age < 2*time.Second || next.Lease == pending.Lease || next.Lease == first.Lease {
		t.Fatal("restart refreshed ages or retained ownership", next)
	}
	old, err := reopened.Commit(context.Background(), testID, pending.Lease, []byte("obsolete"))
	if err != nil || old.Found {
		t.Fatal(old, err)
	}
	if err := reopened.Discard(context.Background(), testID, pending.Lease); err != nil {
		t.Fatal(err)
	}
	busy, err := reopened.Begin(context.Background(), testID, p, time.Second)
	if err != nil || !busy.Busy {
		t.Fatal("old discard retired replacement owner", busy, err)
	}
	publish(t, reopened, testID, next.Lease, []byte("new"))
}

func TestFileAuditExpiredValueCannotReviveOnReopen(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	p := Policy{FreshFor: time.Second, ExpireAfter: 3 * time.Second}
	origin := time.Now().Add(-10 * time.Second)
	f.now = func() time.Time { return origin }
	l := acquire(t, f, testID, p)
	publish(t, f, testID, l.Lease, []byte("expired"))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openFileTest(t, dir, FileOptions{})
	v := acquire(t, reopened, testID, p)
	if v.Found {
		t.Fatal("startup gave an expired generation a new lifetime", v)
	}
}

func TestFileAuditCappedNeverDeadlineSurvivesRepeatedRestart(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	p := Policy{FreshFor: time.Minute, ExpireAfter: Never, Tags: []string{"posts"}}
	l := acquire(t, f, testID, p)
	publish(t, f, testID, l.Lease, []byte("never"))
	if n, err := f.MarkStale(context.Background(), "app", Selector{Kind: "tag", Value: "posts"}, 30*time.Second); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	before := f.memory.entries[testID].Value.(*memoryEntry)
	loaded, deadline := before.loaded, before.expires
	for i := 0; i < 2; i++ {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		f = openFileTest(t, dir, FileOptions{})
		e := f.memory.entries[testID].Value.(*memoryEntry)
		if e.noExpiry || !e.loaded.UTC().Equal(loaded.UTC()) || !e.expires.UTC().Equal(deadline.UTC()) {
			t.Fatal("restart reset or extended a capped Never generation", e)
		}
		if n, err := f.MarkStale(context.Background(), "app", Selector{Kind: "tag", Value: "posts"}, Never); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
}

func TestFileAuditLeaseAdmissionEvictionIsDurable(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{Capacity: 1})
	p := Policy{FreshFor: time.Minute, ExpireAfter: time.Hour}
	l := acquire(t, f, testID, p)
	publish(t, f, testID, l.Lease, []byte("evicted"))
	other := testID
	other.Key = "unpublished replacement"
	acquire(t, f, other, p) // No value is published, but admission evicts a durable value.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f = openFileTest(t, dir, FileOptions{Capacity: 1})
	v := acquire(t, f, testID, p)
	if v.Found {
		t.Fatal("lease-only admission eviction was lost on restart", v)
	}
}

func TestFileAuditLiveClockRollbackPoisonsFastReads(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	l := acquire(t, f, testID, testPolicy)
	publish(t, f, testID, l.Lease, []byte("fresh"))
	before := f.lastWrite
	f.now = func() time.Time { return before.Add(-time.Second) }
	if v, err := f.Begin(context.Background(), testID, testPolicy, time.Second); !errors.Is(err, ErrUnavailable) || v.Found {
		t.Fatal("fast fresh read ignored clock rollback", v, err)
	}
	f.now = time.Now
	if _, err := f.Begin(context.Background(), testID, testPolicy, time.Second); !errors.Is(err, ErrUnavailable) {
		t.Fatal("rollback poison cleared without reopen", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f = openFileTest(t, dir, FileOptions{})
	if v, err := f.Begin(context.Background(), testID, testPolicy, time.Second); err != nil || string(v.Value) != "fresh" {
		t.Fatal(v, err)
	}
}

func TestFileAuditRestartClockEarlierThanDurableWatermarkFails(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	future := time.Now().Add(time.Hour)
	f.now = func() time.Time { return future }
	l := acquire(t, f, testID, testPolicy)
	publish(t, f, testID, l.Lease, []byte("future"))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := OpenFile(dir, FileOptions{}); err == nil {
		reopened.Close()
		t.Fatal("reopened earlier than acknowledged durable clock")
	}
}

func TestFileAuditStagingNeverResurrectsAcknowledgedDeletion(t *testing.T) {
	for _, mode := range []string{"valid-live", "missing-live", "empty-live", "corrupt-live"} {
		t.Run(mode, func(t *testing.T) {
			dir := fileAuditDir(t)
			f := openFileTest(t, dir, FileOptions{})
			l := acquire(t, f, testID, testPolicy)
			publish(t, f, testID, l.Lease, []byte("old backup"))
			wal := filepath.Join(dir, "cache.wal")
			old, err := os.ReadFile(wal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Expire(context.Background(), "app", Selector{Kind: "all"}); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cache.next"), old, 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-live":
				if err := os.Remove(wal); err != nil {
					t.Fatal(err)
				}
			case "empty-live":
				if err := os.Truncate(wal, 0); err != nil {
					t.Fatal(err)
				}
			case "corrupt-live":
				current, err := os.ReadFile(wal)
				if err != nil {
					t.Fatal(err)
				}
				current[len(current)-1] ^= 1
				if err := os.WriteFile(wal, current, 0600); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := OpenFile(dir, FileOptions{})
			if mode != "valid-live" {
				if err == nil {
					reopened.Close()
					t.Fatal("staging bypassed invalid live journal")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if v := acquire(t, reopened, testID, testPolicy); v.Found {
				t.Fatal("uncommitted staging file resurrected deleted data", v)
			}
			if _, err := os.Stat(filepath.Join(dir, "cache.next")); !os.IsNotExist(err) {
				t.Fatal("unused staging file retained", err)
			}
		})
	}
}

func TestFileAuditCanceledGateWaitDoesNotMutate(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	f.gate <- struct{}{}
	release := sync.OnceFunc(func() { <-f.gate })
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.Begin(ctx, testID, testPolicy, time.Second); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		release()
		t.Fatal("waiting operation ignored context")
	}
	release()
	if keys, _ := f.Stats(); keys != 0 {
		t.Fatal("canceled waiter acquired a lease", keys)
	}
}

func TestFileAuditRejectsSemanticallyExtendedDeadlinesWithValidChecksums(t *testing.T) {
	for _, mode := range []string{"finite-expiry", "future-freshness", "capped-never"} {
		t.Run(mode, func(t *testing.T) {
			dir := fileAuditDir(t)
			f := openFileTest(t, dir, FileOptions{})
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Second)
			p := Policy{FreshFor: time.Second, ExpireAfter: 5 * time.Second}
			d := &diskEntry{ID: testID, Policy: p, Value: []byte("data"), Generation: strings.Repeat("a", 64), Loaded: at, Fresh: at.Add(time.Second), Expires: at.Add(5 * time.Second)}
			switch mode {
			case "finite-expiry":
				d.Expires = at.Add(time.Hour)
			case "future-freshness":
				d.Policy.ExpireAfter = Never
				d.NoExpiry = true
				d.Expires = time.Time{}
				d.Loaded = at.Add(-10 * time.Second)
				d.Fresh = at.Add(500 * time.Millisecond)
			case "capped-never":
				d.Policy.ExpireAfter = Never
				d.Fresh = at
				d.Expires = at.Add(2 * maxDuration)
			}
			var journal bytes.Buffer
			journal.WriteString(fileMagic)
			if _, err := emitTransaction(&journal, 1, at, []diskRecord{{Kind: "put", ID: testID, Entry: d}}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cache.wal"), journal.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if reopened, err := OpenFile(dir, FileOptions{}); err == nil {
				reopened.Close()
				t.Fatal("valid checksum hid invalid generation deadlines", mode)
			}
		})
	}
}

func TestFileAuditMarkCannotTargetAnotherGeneration(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{})
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	d := diskEntry{ID: testID, Policy: testPolicy, Value: []byte("one"), Generation: strings.Repeat("a", 64), Loaded: at, Fresh: at.Add(time.Second), Expires: at.Add(5 * time.Second)}
	var journal bytes.Buffer
	journal.WriteString(fileMagic)
	if _, err := emitTransaction(&journal, 1, at, []diskRecord{{Kind: "put", ID: testID, Entry: &d}}); err != nil {
		t.Fatal(err)
	}
	mark := d
	mark.Generation = strings.Repeat("b", 64)
	mark.Value = nil
	mark.Fresh = at
	if _, err := emitTransaction(&journal, 2, at, []diskRecord{{Kind: "mark", ID: testID, Entry: &mark}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cache.wal"), journal.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := OpenFile(dir, FileOptions{}); err == nil {
		reopened.Close()
		t.Fatal("cross-generation stale mark accepted")
	}
}
