//go:build linux || darwin

package shared

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
)

// The lock inode is separate from the atomically replaced journal. Never unlink
// it, even on failure/Close: another opener must always lock that same inode.
func acquireFileLock(path string) (io.Closer, error) {
	existing, err := os.Lstat(path)
	if err == nil && !privateLockFile(existing) {
		return nil, fileLockInvalid()
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: cannot inspect file-store lock", ErrUnavailable)
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EISDIR) {
			return nil, fileLockInvalid()
		}
		return nil, fmt.Errorf("%w: cannot open file-store lock", ErrUnavailable)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("%w: cannot own file-store lock", ErrUnavailable)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil || !privateLockFile(opened) {
		return nil, fileLockInvalid()
	}
	if existing != nil && !os.SameFile(existing, opened) {
		return nil, fmt.Errorf("%w: file-store lock changed during open", ErrUnavailable)
	}
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: file store is already open", ErrUnavailable)
		}
		return nil, fmt.Errorf("%w: cannot acquire file-store lock", ErrUnavailable)
	}
	opened, err = file.Stat()
	if err != nil || !privateLockFile(opened) {
		return nil, fileLockInvalid()
	}
	current, err := os.Lstat(path)
	if err != nil || !privateLockFile(current) || !os.SameFile(opened, current) {
		return nil, fmt.Errorf("%w: file-store lock changed during acquisition", ErrUnavailable)
	}
	keep = true
	return &heldFileLock{file: file}, nil
}

func privateLockFile(info os.FileInfo) bool {
	return privateDataFile(info) && info.Size() == 0
}

func privateDataFile(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode() != 0600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func privateDataDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode() != os.ModeDir|0700 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
func fileLockInvalid() error {
	return fmt.Errorf("%w: file-store lock must be an owned empty regular 0600 file with one link", ErrInvalid)
}

type heldFileLock struct {
	file *os.File
	once sync.Once
	err  error
}

func (l *heldFileLock) Close() error {
	l.once.Do(func() {
		// Closing the descriptor releases the kernel lock. A separate LOCK_UN
		// call is unnecessary and would create an unlocked-before-close window.
		if err := l.file.Close(); err != nil {
			l.err = fmt.Errorf("%w: cannot close file-store lock", ErrUnavailable)
		}
	})
	return l.err
}
