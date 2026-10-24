//go:build !linux && !darwin

package shared

import (
	"fmt"
	"io"
	"os"
)

func acquireFileLock(string) (io.Closer, error) {
	return nil, fmt.Errorf("%w: file backend requires Linux or macOS kernel file locks", ErrUnavailable)
}

func privateDataFile(os.FileInfo) bool      { return false }
func privateDataDirectory(os.FileInfo) bool { return false }
