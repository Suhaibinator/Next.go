//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"
)

func TestMultipartBoundsOrderingFileHandlesAndSnapshots(t *testing.T) {
	for _, limit := range []int64{0, 255, maxMultipartBytes + 1} {
		if _, err := NewMultipart(limit); err == nil {
			t.Fatal(limit)
		}
	}
	form, err := NewMultipart(16384)
	if err != nil {
		t.Fatal(err)
	}
	if err := form.AddField("topic", "one"); err != nil {
		t.Fatal(err)
	}
	if err := form.AddField("topic", "two\nlines"); err != nil {
		t.Fatal(err)
	}
	file := testBrowserFile(t, []byte{0, 1, 255}, "bytes.bin")
	if err := form.AddFile("files", file); err != nil {
		t.Fatal(err)
	}
	budget, _ := form.BudgetBytes()
	if budget <= 3 {
		t.Fatal("missing envelope accounting")
	}
	body, err := form.formData()
	if err != nil {
		t.Fatal(err)
	}
	if body.Call("getAll", "topic").Length() != 2 || body.Call("get", "files").Get("size").Int() != 3 {
		t.Fatal("parts lost")
	}
	before := budget
	if err := form.AddField("bad\nname", "value"); err == nil {
		t.Fatal("invalid field")
	}
	if got, _ := form.BudgetBytes(); got != before {
		t.Fatal("invalid add mutated builder")
	}
	if err := form.AddField("large", strings.Repeat("x", 16384)); err == nil {
		t.Fatal("budget bypass")
	}
	if _, err := json.Marshal(form); err == nil {
		t.Fatal("opaque form serialized")
	}
	form.Close()
	form.Close()
	if _, err := form.BudgetBytes(); err == nil {
		t.Fatal("closed form reusable")
	}
	if err := form.AddField("after", "close"); err == nil {
		t.Fatal("closed form mutated")
	}
	if body.Call("getAll", "topic").Length() != 2 || body.Call("get", "files").Get("name").String() != "bytes.bin" {
		t.Fatal("closing builder mutated snapshot")
	}
	for _, name := range []string{"", "bad\nname", "bad\x00name"} {
		f, _ := NewMultipart(16384)
		if err := f.AddFile("files", testBrowserFile(t, nil, name)); err == nil {
			t.Fatal("invalid filename", name)
		}
	}
	var empty Multipart
	if err := empty.AddField("x", "y"); err == nil {
		t.Fatal("zero builder accepted")
	}
}
func TestMultipartPartAndFileCountsAreAtomic(t *testing.T) {
	form, _ := NewMultipart(maxMultipartBytes)
	for i := 0; i < 128; i++ {
		if err := form.AddField("repeated", ""); err != nil {
			t.Fatal(i, err)
		}
	}
	before, _ := form.BudgetBytes()
	if err := form.AddField("extra", ""); err == nil {
		t.Fatal("part quota bypass")
	}
	after, _ := form.BudgetBytes()
	if before != after {
		t.Fatal("failed part mutated budget")
	}
	form, _ = NewMultipart(maxMultipartBytes)
	file := testBrowserFile(t, nil, "empty")
	for i := 0; i < 64; i++ {
		if err := form.AddFile("files", file); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := form.AddFile("files", file); err == nil {
		t.Fatal("file quota bypass")
	}
}
func TestFetchMultipartUsesNativeFormDataAndRejectsAmbiguousOptions(t *testing.T) {
	form, _ := NewMultipart(16384)
	_ = form.AddField("label", "value")
	_ = form.AddFile("files", testBrowserFile(t, []byte{255}, "a.bin"))
	requests := 0
	mockBrowserFetch(t, func(args []js.Value) any {
		requests++
		options := args[1]
		if !options.Get("body").InstanceOf(js.Global().Get("FormData")) || !options.Get("headers").Call("get", "content-type").IsNull() {
			t.Error("body/boundary policy lost")
		}
		form.Close()
		if options.Get("body").Call("get", "files").Get("size").Int() != 1 {
			t.Error("snapshot lost")
		}
		return browserResponse(nil, 200, nil)
	})
	if _, err := Fetch(context.Background(), "/upload", FetchOptions{Method: "POST", Multipart: form}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := NewMultipart(4096)
	for _, options := range []FetchOptions{{Method: "POST", Multipart: fresh, Body: []byte{}}, {Multipart: fresh}, {Method: "HEAD", Multipart: fresh}, {Method: "POST", Multipart: fresh, Headers: map[string]string{"content-type": "multipart/form-data"}}, {Method: "POST", Multipart: form}} {
		if _, err := Fetch(context.Background(), "/upload", options); err == nil {
			t.Fatal("ambiguous form dispatched", options.Method)
		}
	}
	if _, err := Fetch(nil, "/upload", FetchOptions{}); err == nil {
		t.Fatal("nil context")
	}
	if requests != 1 {
		t.Fatal(requests)
	}
}
