package shared

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openFileTest(t *testing.T, dir string, o FileOptions) *File {
	t.Helper()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	f, e := OpenFile(dir, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := f.Close(); e != nil {
			t.Error(e)
		}
	})
	return f
}
func TestFileRestartKeepsValuesAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	f := openFileTest(t, dir, FileOptions{})
	ctx := context.Background()
	p := Policy{FreshFor: time.Minute, ExpireAfter: time.Hour, Tags: []string{"tag"}, Paths: []string{"/posts/a"}}
	for _, key := range []string{"a", "b"} {
		id := testID
		id.Key = key
		l := acquire(t, f, id, p)
		publish(t, f, id, l.Lease, []byte(key))
	}
	if n, e := f.Expire(ctx, "app", Selector{"key", "a"}); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if n, e := f.MarkStale(ctx, "app", Selector{"key", "b"}, 30*time.Second); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	f = openFileTest(t, dir, FileOptions{})
	a := testID
	a.Key = "a"
	v, e := f.Begin(ctx, a, p, time.Second)
	if e != nil || v.Found || v.Lease == "" {
		t.Fatal(v, e)
	}
	b := testID
	b.Key = "b"
	v, e = f.Begin(ctx, b, p, time.Second)
	if e != nil || !v.Found || string(v.Value) != "b" || v.FreshFor != 0 || v.ExpiresIn <= 0 || v.ExpiresIn > 30*time.Second {
		t.Fatal(v, e)
	}
}
func TestFileCompactionAndRejectedAdmission(t *testing.T) {
	dir := t.TempDir()
	f := openFileTest(t, dir, FileOptions{MaxDiskBytes: 8192})
	ctx := context.Background()
	p := Policy{FreshFor: 0, ExpireAfter: Never}
	for i := 0; i < 20; i++ {
		v := acquire(t, f, testID, p)
		publish(t, f, testID, v.Lease, bytes.Repeat([]byte{byte(i)}, 700))
	}
	if s, e := os.Stat(filepath.Join(dir, "cache.wal")); e != nil || s.Size() > 8192 {
		t.Fatal(s, e)
	}
	l := acquire(t, f, testID, p)
	if _, e := f.Commit(ctx, testID, l.Lease, make([]byte, 16000)); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if e := f.Release(ctx, testID, l.Lease); e != nil {
		t.Fatal(e)
	}
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	f = openFileTest(t, dir, FileOptions{MaxDiskBytes: 8192})
	v := acquire(t, f, testID, p)
	if !v.Found || len(v.Value) != 700 || v.Value[0] != 19 {
		t.Fatal(v)
	}
}
func TestFileUncertainIOPoisonsUntilReopen(t *testing.T) {
	dir := t.TempDir()
	f := openFileTest(t, dir, FileOptions{})
	l := acquire(t, f, testID, testPolicy)
	if e := f.log.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Commit(context.Background(), testID, l.Lease, []byte("value")); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if _, e := f.Begin(context.Background(), testID, testPolicy, time.Second); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	// Closing an already faulted descriptor reports the close error, but still
	// releases the ownership lock. The already-complete empty journal can reopen.
	_ = f.Close()
	f.log = nil
	f = openFileTest(t, dir, FileOptions{})
	v := acquire(t, f, testID, testPolicy)
	if v.Found {
		t.Fatal(v)
	}
}
func TestFileMissingAndTruncatedJournalFailClosed(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "tail"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			f := openFileTest(t, dir, FileOptions{})
			l := acquire(t, f, testID, testPolicy)
			publish(t, f, testID, l.Lease, []byte("value"))
			if e := f.Close(); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(dir, "cache.wal")
			var e error
			switch kind {
			case "missing":
				e = os.Remove(path)
			case "empty":
				e = os.Truncate(path, 0)
			case "tail":
				s, _ := os.Stat(path)
				e = os.Truncate(path, s.Size()-1)
			}
			if e != nil {
				t.Fatal(e)
			}
			if other, e := OpenFile(dir, FileOptions{}); e == nil {
				other.Close()
				t.Fatal("corrupt store accepted")
			}
		})
	}
}

func TestFileSyncFailureAndCanceledAcknowledgment(t *testing.T) {
	for _, mode := range []string{"failure", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			f := openFileTest(t, dir, FileOptions{})
			l := acquire(t, f, testID, testPolicy)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.syncFile = func(file *os.File) error {
				if mode == "failure" {
					return errors.New("injected disk failure")
				}
				e := file.Sync()
				cancel()
				return e
			}
			_, e := f.Commit(ctx, testID, l.Lease, []byte("committed"))
			if mode == "failure" {
				if !errors.Is(e, ErrUnavailable) {
					t.Fatal(e)
				}
				if _, e = f.Begin(context.Background(), testID, testPolicy, time.Second); !errors.Is(e, ErrUnavailable) {
					t.Fatal(e)
				}
			} else {
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				v, e := f.Begin(context.Background(), testID, testPolicy, time.Second)
				if e != nil || string(v.Value) != "committed" {
					t.Fatal(v, e)
				}
			}
			if e = f.Close(); e != nil {
				t.Fatal(e)
			}
			f = openFileTest(t, dir, FileOptions{})
			v, e := f.Begin(context.Background(), testID, testPolicy, time.Second)
			if e != nil || string(v.Value) != "committed" {
				t.Fatal(v, e)
			}
		})
	}
}
func TestFileCompactionFailureRetainsLockAndFailsClosed(t *testing.T) {
	for _, stage := range []string{"file-sync", "rename", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			f := openFileTest(t, dir, FileOptions{})
			p := Policy{FreshFor: 0, ExpireAfter: Never}
			l := acquire(t, f, testID, p)
			publish(t, f, testID, l.Lease, bytes.Repeat([]byte("a"), 700))
			f.maxDisk = f.size + 128
			injected := errors.New("injected compaction failure")
			switch stage {
			case "file-sync":
				f.syncFile = func(*os.File) error { return injected }
			case "rename":
				f.rename = func(string, string) error { return injected }
			case "directory-sync":
				f.syncDir = func(string) error { return injected }
			}
			l = acquire(t, f, testID, p)
			if _, e := f.Commit(context.Background(), testID, l.Lease, bytes.Repeat([]byte("b"), 700)); !errors.Is(e, ErrUnavailable) {
				t.Fatal(e)
			}
			if other, e := OpenFile(dir, FileOptions{}); e == nil {
				other.Close()
				t.Fatal("poisoned writer released lock")
			}
			if _, e := f.Begin(context.Background(), testID, p, time.Second); !errors.Is(e, ErrUnavailable) {
				t.Fatal(e)
			}
			if e := f.Close(); e != nil {
				t.Fatal(e)
			}
			f = openFileTest(t, dir, FileOptions{})
			v := acquire(t, f, testID, p)
			want := byte('a')
			if stage == "directory-sync" {
				want = 'b'
			}
			if !v.Found || len(v.Value) != 700 || v.Value[0] != want {
				t.Fatal(v)
			}
			if _, e := os.Stat(filepath.Join(dir, "cache.next")); !os.IsNotExist(e) {
				t.Fatal("staging file not cleaned", e)
			}
		})
	}
}
