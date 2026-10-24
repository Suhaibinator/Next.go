//go:build linux || darwin

package shared

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func lockBusy(err error) bool {
	return errors.Is(err, ErrUnavailable) && strings.Contains(err.Error(), "already open")
}

func TestFileLockPrivateStableInodeAndIdempotentClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.lock")
	first, err := acquireFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	before, err := os.Lstat(path)
	if err != nil || !privateLockFile(before) {
		t.Fatal(before, err)
	}
	if second, err := acquireFileLock(path); !lockBusy(err) || second != nil {
		if second != nil {
			second.Close()
		}
		t.Fatal("same process did not conflict", err)
	}
	held := first.(*heldFileLock)
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, held.file.Fd(), syscall.F_GETFD, 0)
	if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatal("descriptor is not close-on-exec", flags, errno)
	}
	var closeErrors [16]error
	var group sync.WaitGroup
	for i := range closeErrors {
		group.Add(1)
		go func(i int) { defer group.Done(); closeErrors[i] = first.Close() }(i)
	}
	group.Wait()
	for _, err := range closeErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := held.file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("descriptor remained open", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || !privateLockFile(after) {
		t.Fatal("close removed/replaced lock inode", after, err)
	}
	for i := 0; i < 20; i++ {
		next, err := acquireFileLock(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := next.Close(); err != nil {
			t.Fatal(err)
		}
	}
	last, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, last) {
		t.Fatal("reopening replaced stable inode", err)
	}
}

func TestFileLockConcurrentOpenHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.lock")
	type result struct {
		lock io.Closer
		err  error
	}
	results := make(chan result, 32)
	for i := 0; i < cap(results); i++ {
		go func() { lock, err := acquireFileLock(path); results <- result{lock, err} }()
	}
	winners := 0
	for i := 0; i < cap(results); i++ {
		r := <-results
		if r.err == nil {
			winners++
			defer r.lock.Close()
		} else if !lockBusy(r.err) {
			t.Fatal(r.err)
		}
	}
	if winners != 1 {
		t.Fatal("exclusive lease winners", winners)
	}
}

func TestFileLockRejectsUnsafeFilesWithoutChangingThem(t *testing.T) {
	for _, kind := range []string{"public", "read-only", "nonempty", "directory", "symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.lock")
			target := filepath.Join(dir, "other")
			switch kind {
			case "public":
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "read-only":
				if err := os.WriteFile(path, nil, 0400); err != nil {
					t.Fatal(err)
				}
			case "nonempty":
				if err := os.WriteFile(path, []byte("do not overwrite"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.WriteFile(target, []byte("target must stay unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := acquireFileLock(path)
			if !errors.Is(err, ErrInvalid) || lock != nil {
				if lock != nil {
					lock.Close()
				}
				t.Fatal("unsafe file accepted", kind, err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() {
				t.Fatal("rejection changed file", before, after, err)
			}
			if kind == "nonempty" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "do not overwrite" {
					t.Fatal(string(data), err)
				}
			}
			if kind == "symlink" {
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "target must stay unchanged" {
					t.Fatal(string(data), err)
				}
			}
		})
	}
}

type modifiedLockInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
	mode os.FileMode
}

func (i modifiedLockInfo) Sys() any          { return i.stat }
func (i modifiedLockInfo) Mode() os.FileMode { return i.mode }
func (i modifiedLockInfo) IsDir() bool       { return i.mode.IsDir() }

func TestFileLockPrivateDataOwnershipHelpers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "data")
	if err := os.WriteFile(path, []byte("journal bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !privateDataFile(file) || privateLockFile(file) || !privateDataDirectory(directory) || privateDataFile(directory) || privateDataDirectory(file) || privateDataFile(nil) || privateDataDirectory(nil) {
		t.Fatalf("file/directory privacy shape: file=%v directory=%v filePrivate=%v directoryPrivate=%v", file.Mode(), directory.Mode(), privateDataFile(file), privateDataDirectory(directory))
	}
	fileStat := *file.Sys().(*syscall.Stat_t)
	fileStat.Uid++
	if privateDataFile(modifiedLockInfo{file, &fileStat, file.Mode()}) {
		t.Fatal("accepted different file owner")
	}
	dirStat := *directory.Sys().(*syscall.Stat_t)
	dirStat.Uid++
	if privateDataDirectory(modifiedLockInfo{directory, &dirStat, directory.Mode()}) {
		t.Fatal("accepted different directory owner")
	}
	fileStat = *file.Sys().(*syscall.Stat_t)
	fileStat.Nlink = 2
	if privateDataFile(modifiedLockInfo{file, &fileStat, file.Mode()}) {
		t.Fatal("accepted multiple file links")
	}
	fileStat = *file.Sys().(*syscall.Stat_t)
	for _, mode := range []os.FileMode{0400, 0644, 0600 | os.ModeSetuid, 0600 | os.ModeSetgid, 0600 | os.ModeSticky} {
		if privateDataFile(modifiedLockInfo{file, &fileStat, mode}) {
			t.Fatal("accepted nonprivate/special file mode", mode)
		}
	}
	dirStat = *directory.Sys().(*syscall.Stat_t)
	for _, mode := range []os.FileMode{os.ModeDir | 0755, os.ModeDir | 0701, os.ModeDir | 0700 | os.ModeSticky} {
		if privateDataDirectory(modifiedLockInfo{directory, &dirStat, mode}) {
			t.Fatal("accepted nonprivate/special directory mode", mode)
		}
	}
}

const lockHelperMode = "NEXTGO_FILE_LOCK_TEST_MODE"
const lockHelperPath = "NEXTGO_FILE_LOCK_TEST_PATH"

// TestFileLockProcessHelper is invoked only in a dedicated child test binary.
func TestFileLockProcessHelper(t *testing.T) {
	mode := os.Getenv(lockHelperMode)
	if mode == "" {
		t.Skip("subprocess helper")
	}
	if mode == "idle" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	lock, err := acquireFileLock(os.Getenv(lockHelperPath))
	if mode == "probe" {
		if lockBusy(err) {
			fmt.Println("busy")
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		fmt.Println("acquired")
		return
	}
	if mode != "hold" {
		t.Fatal("unknown helper mode")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
func probeFileLock(t *testing.T, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileLockProcessHelper$")
	cmd.Env = append(os.Environ(), lockHelperMode+"=probe", lockHelperPath+"="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	return string(out)
}
func startLockHelper(t *testing.T, path, mode string) (*exec.Cmd, chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileLockProcessHelper$")
	cmd.Env = append(os.Environ(), lockHelperMode+"="+mode, lockHelperPath+"="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		input.Close()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		input.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		input.Close()
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatal("unexpected helper startup", line)
		}
	case <-ctx.Done():
		t.Fatal("lock helper startup timeout")
	case err := <-done:
		t.Fatal("lock helper exited early", err)
	}
	return cmd, done
}

func TestFileLockKernelExcludesOtherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.lock")
	lock, err := acquireFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if out := probeFileLock(t, path); !strings.Contains(out, "busy\n") {
		t.Fatal("other process acquired held lock", out)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if out := probeFileLock(t, path); !strings.Contains(out, "acquired\n") {
		t.Fatal("closed lock remained held", out)
	}
}
func TestFileLockKernelReleasesKilledOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.lock")
	cmd, done := startLockHelper(t, path, "hold")
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if lock, err := acquireFileLock(path); !lockBusy(err) {
		if lock != nil {
			lock.Close()
		}
		t.Fatal("child did not hold lock", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("killed helper exited successfully")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killed helper did not exit")
	}
	lock, err := acquireFileLock(path)
	if err != nil {
		t.Fatal("kernel retained dead owner's lock", err)
	}
	defer lock.Close()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("crash replaced lock inode", err)
	}
}
func TestFileLockDoesNotLeakAcrossExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.lock")
	lock, err := acquireFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	_, _ = startLockHelper(t, path, "idle")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	newLock, err := acquireFileLock(path)
	if err != nil {
		t.Fatal("exec child inherited parent lock descriptor", err)
	}
	defer newLock.Close()
}
