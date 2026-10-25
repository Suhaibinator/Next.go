//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"syscall/js"
)

var (
	ErrFileRead     = errors.New("nextgo: could not read the selected file")
	ErrFileTooLarge = errors.New("nextgo: file exceeds the read byte limit")
	ErrFileReadBusy = errors.New("nextgo: file reader limit reached")
)

// File is an immutable browser File handle. It is browser-local state, not a
// serializable DTO, path, authorization capability or proof of content type.
type File struct {
	value js.Value
	info  FileInfo
}
type FileInfo struct {
	Name, MediaType    string
	Size, LastModified int64
}

func (f File) Info() FileInfo { return f.info }
func (f File) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("nextgo: browser files cannot be serialized; explicitly read bounded bytes")
}
func SameFiles(a, b []File) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].value.Equal(b[i].value) {
			return false
		}
	}
	return true
}

func browserFile(value js.Value) (File, error) {
	var zero File
	constructor := js.Global().Get("File")
	if value.IsNull() || value.IsUndefined() || constructor.Type() != js.TypeFunction || !value.InstanceOf(constructor) {
		return zero, ErrFileRead
	}
	size, modified := value.Get("size").Float(), value.Get("lastModified").Float()
	if math.IsNaN(size) || math.IsInf(size, 0) || size < 0 || size > 9007199254740991 || math.Trunc(size) != size || math.IsNaN(modified) || math.IsInf(modified, 0) || math.Abs(modified) > 9007199254740991 || math.Trunc(modified) != modified {
		return zero, ErrFileRead
	}
	info := FileInfo{Name: value.Get("name").String(), MediaType: value.Get("type").String(), Size: int64(size), LastModified: int64(modified)}
	if len(info.Name) > 4096 || len(info.MediaType) > 1024 {
		return zero, fmt.Errorf("nextgo: file metadata limit exceeded")
	}
	return File{value: value, info: info}, nil
}

const maxFileReadBytes = 8 << 20
const maxFileReaders = 8

var activeFileReads int

type fileReadResult struct {
	buffer js.Value
	err    error
}

// Bytes reads an owned byte slice from a browser-selected File. Call it from a
// goroutine, with an explicit 1-byte to 8-MiB limit. Cancellation aborts its private
// FileReader. Its listener is removed before releasing the Go callback, including
// setup failure and cancellation; queued events then have no owned listener.
func (f File) Bytes(ctx context.Context, limit int64) (data []byte, readErr error) {
	activity, err := beginRefreshActivity()
	if err != nil {
		return nil, err
	}
	defer activity.Done()
	var cleanup func()
	var reader js.Value
	admitted := false
	defer func() {
		if recover() != nil {
			data = nil
			readErr = ErrFileRead
		}
		if cleanup != nil {
			if readErr != nil {
				_ = lifetimeGuard(func() {
					if reader.Get("readyState").Int() == 1 {
						reader.Call("abort")
					}
				})
			}
			_ = lifetimeGuard(cleanup)
		}
		if admitted {
			activeFileReads--
		}
	}()
	if ctx == nil || limit <= 0 || limit > maxFileReadBytes {
		return nil, fmt.Errorf("nextgo: file read requires a context and 1-byte to 8-MiB limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	actual, err := browserFile(f.value)
	if err != nil || actual.info != f.info {
		return nil, ErrFileRead
	}
	if f.info.Size > limit {
		return nil, ErrFileTooLarge
	}
	if activeFileReads >= maxFileReaders {
		return nil, ErrFileReadBusy
	}
	constructor := js.Global().Get("FileReader")
	if constructor.Type() != js.TypeFunction {
		return nil, ErrFileRead
	}
	activeFileReads++
	admitted = true
	reader = constructor.New()
	done := make(chan fileReadResult, 1)
	finished := false
	var callback js.Func
	cleanup = func() {
		if finished {
			return
		}
		finished = true
		_ = lifetimeGuard(func() { reader.Call("removeEventListener", "loadend", callback) })
		callback.Release()
	}
	callback = js.FuncOf(func(js.Value, []js.Value) any {
		if finished {
			return nil
		}
		result := fileReadResult{}
		if err := lifetimeGuard(func() {
			if reader.Get("error").Truthy() {
				result.err = ErrFileRead
				return
			}
			result.buffer = reader.Get("result")
			if result.buffer.IsNull() || result.buffer.IsUndefined() {
				result.err = ErrFileRead
			}
		}); err != nil {
			result.err = ErrFileRead
		}
		cleanup()
		done <- result
		return nil
	})
	reader.Call("addEventListener", "loadend", callback)
	reader.Call("readAsArrayBuffer", f.value)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if result.err != nil {
			return nil, result.err
		}
		length := result.buffer.Get("byteLength").Int()
		if int64(length) != f.info.Size || int64(length) > limit || length < 0 {
			return nil, ErrFileRead
		}
		data = make([]byte, length)
		if js.CopyBytesToGo(data, js.Global().Get("Uint8Array").New(result.buffer)) != length {
			return nil, ErrFileRead
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}
}
