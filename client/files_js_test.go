//go:build js && wasm

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"syscall/js"
	"testing"
	"time"
)

func testBrowserFile(t *testing.T, data []byte, name string) File {
	t.Helper()
	array := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(array, data)
	value := js.Global().Get("File").New([]any{array}, name, map[string]any{"type": "application/octet-stream", "lastModified": 123})
	file, err := browserFile(value)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
func fakeFileReader(t *testing.T, mode string) {
	t.Helper()
	global := js.Global()
	original := global.Get("FileReader")
	global.Set("fileReadMode", mode)
	global.Set("fileReaderListeners", 0)
	global.Set("fileReaderAborts", 0)
	global.Set("testReaders", global.Get("Array").New())
	constructor := global.Get("Function").New(`return class extends EventTarget {
 constructor(){super();this.readyState=0;this.error=null;this.result=null;this.listeners=new Set();globalThis.testReaders.push(this)}
 addEventListener(type,fn){super.addEventListener(type,fn);if(type==='loadend'&&!this.listeners.has(fn)){this.listeners.add(fn);globalThis.fileReaderListeners++}}
 removeEventListener(type,fn){super.removeEventListener(type,fn);if(type==='loadend'&&this.listeners.delete(fn))globalThis.fileReaderListeners--}
 readAsArrayBuffer(file){if(globalThis.fileReadMode==='throw')throw new Error('private setup');this.readyState=1;if(globalThis.fileReadMode==='hold')return;file.arrayBuffer().then(buffer=>{if(this.readyState!==1)return;this.readyState=2;if(globalThis.fileReadMode==='error'){this.error=new Error('private read')}else{this.result=globalThis.fileReadMode==='mismatch'?buffer.slice(1):buffer}this.dispatchEvent(new Event('loadend'))})}
 abort(){if(this.readyState===1){globalThis.fileReaderAborts++;this.readyState=2;this.result=null;this.dispatchEvent(new Event('loadend'))}}
 }`).Invoke()
	global.Set("FileReader", constructor)
	t.Cleanup(func() {
		global.Set("FileReader", original)
		if activeFileReads != 0 || global.Get("fileReaderListeners").Int() != 0 {
			t.Errorf("reader resources retained: %d / %d", activeFileReads, global.Get("fileReaderListeners").Int())
		}
	})
}
func TestFileBytesAreOwnedBoundedAndCannotSerializeBrowserHandles(t *testing.T) {
	fakeFileReader(t, "normal")
	file := testBrowserFile(t, []byte{0, 1, 255, 128}, "bytes.bin")
	info := file.Info()
	if info.Name != "bytes.bin" || info.Size != 4 || info.LastModified != 123 {
		t.Fatal(info)
	}
	info.Name = "outside"
	if file.Info().Name == "outside" {
		t.Fatal("metadata changed")
	}
	if _, err := json.Marshal(file); err == nil {
		t.Fatal("opaque browser handle serialized")
	}
	if !SameFiles([]File{file}, []File{file}) || SameFiles([]File{file}, []File{testBrowserFile(t, []byte{0, 1, 255, 128}, "bytes.bin")}) {
		t.Fatal("file identity lost")
	}
	if _, err := file.Bytes(context.Background(), 3); !errors.Is(err, ErrFileTooLarge) {
		t.Fatal(err)
	}
	for _, limit := range []int64{0, -1, maxFileReadBytes + 1} {
		if _, err := file.Bytes(context.Background(), limit); err == nil {
			t.Fatal(limit)
		}
	}
	data, err := file.Bytes(context.Background(), 4)
	if err != nil || !bytes.Equal(data, []byte{0, 1, 255, 128}) {
		t.Fatal(data, err)
	}
	data[0] = 42
	again, err := file.Bytes(context.Background(), 4)
	if err != nil || again[0] != 0 {
		t.Fatal("read aliases returned bytes", again, err)
	}
	empty, err := testBrowserFile(t, nil, "empty").Bytes(context.Background(), 1)
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}
func TestFileReadFailureAndCancellationReleaseListeners(t *testing.T) {
	for _, mode := range []string{"throw", "error", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			fakeFileReader(t, mode)
			file := testBrowserFile(t, []byte("data"), "a.txt")
			if _, err := file.Bytes(context.Background(), 10); !errors.Is(err, ErrFileRead) {
				t.Fatal(err)
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		fakeFileReader(t, "hold")
		file := testBrowserFile(t, []byte("data"), "a.txt")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := file.Bytes(ctx, 10); done <- err }()
		for deadline := time.Now().Add(time.Second); activeFileReads != 1 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if js.Global().Get("fileReaderAborts").Int() != 1 {
			t.Fatal("read not aborted")
		}
		reader := js.Global().Get("testReaders").Index(0)
		reader.Call("dispatchEvent", js.Global().Get("Event").New("loadend"))
	})
}
func TestFileReadAdmissionIsBoundedUntilCallersReturn(t *testing.T) {
	fakeFileReader(t, "hold")
	file := testBrowserFile(t, []byte("data"), "a.txt")
	var group sync.WaitGroup
	cancels := []context.CancelFunc{}
	for i := 0; i < maxFileReaders; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := file.Bytes(ctx, 10)
			if !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		}()
	}
	for deadline := time.Now().Add(time.Second); activeFileReads != maxFileReaders && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if _, err := file.Bytes(context.Background(), 10); !errors.Is(err, ErrFileReadBusy) {
		t.Fatal(err)
	}
	for _, cancel := range cancels {
		cancel()
	}
	group.Wait()
}
