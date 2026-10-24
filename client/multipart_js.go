//go:build js && wasm

package client

import (
	"fmt"
	"strings"
	"syscall/js"
	"unicode/utf8"
)

// Multipart owns bounded text and browser File references. Each Fetch builds a
// fresh FormData snapshot, so later edits/Close do not mutate an in-flight body.
// BudgetBytes includes conservative framing/escaping allowances, not a measured
// browser wire length. The receiver must also enforce its actual request limit.
type Multipart struct {
	parts         []multipartPart
	budget, limit int64
	files         int
	closed        bool
}
type multipartPart struct {
	name, text string
	file       *File
}

const maxMultipartBytes = 64 << 20

func NewMultipart(maxBytes int64) (*Multipart, error) {
	if maxBytes < 256 || maxBytes > maxMultipartBytes {
		return nil, fmt.Errorf("nextgo: multipart budget must be 256 bytes to 64 MiB")
	}
	return &Multipart{limit: maxBytes, budget: 256}, nil
}
func (m *Multipart) open() bool {
	return m != nil && !m.closed && m.limit >= 256 && m.limit <= maxMultipartBytes
}
func (m *Multipart) add(part multipartPart, cost int64) error {
	if !m.open() {
		return fmt.Errorf("nextgo: multipart body is closed or uninitialized")
	}
	if len(m.parts) >= 128 || cost < 0 || cost > m.limit-m.budget {
		return fmt.Errorf("nextgo: multipart part or byte budget exceeded")
	}
	m.parts = append(m.parts, part)
	m.budget += cost
	return nil
}

// AddField preserves repeated names and order. Values must be valid UTF-8.
func (m *Multipart) AddField(name, value string) error {
	if !validFormName(name) || !utf8.ValidString(value) {
		return fmt.Errorf("nextgo: invalid multipart field")
	}
	return m.add(multipartPart{name: name, text: value}, 1024+3*int64(len(name))+2*int64(len(value)))
}

// AddFile sends an immutable selected File without copying its bytes into Go.
// File names must be nonempty and contain no NUL/CR/LF; names remain untrusted.
func (m *Multipart) AddFile(name string, file File) error {
	if !m.open() {
		return fmt.Errorf("nextgo: multipart body is closed or uninitialized")
	}
	if !validFormName(name) || m.files >= 64 {
		return fmt.Errorf("nextgo: invalid multipart file field or file limit exceeded")
	}
	actual, err := browserFile(file.value)
	if err != nil || actual.info != file.info {
		return ErrFileRead
	}
	info := file.Info()
	if info.Name == "" || strings.ContainsAny(info.Name, "\x00\r\n") {
		return fmt.Errorf("nextgo: invalid multipart file name")
	}
	cost := 1024 + 3*int64(len(name)+len(info.Name)+len(info.MediaType)) + info.Size
	if err := m.add(multipartPart{name: name, file: &file}, cost); err != nil {
		return err
	}
	m.files++
	return nil
}
func (m *Multipart) BudgetBytes() (int64, error) {
	if !m.open() {
		return 0, fmt.Errorf("nextgo: multipart body is closed or uninitialized")
	}
	return m.budget, nil
}

// Close drops builder-owned handles. It does not cancel already dispatched Fetch;
// use the request context for cancellation. Closing twice is harmless.
func (m *Multipart) Close() {
	if m == nil || m.closed {
		return
	}
	m.closed = true
	m.parts = nil
	m.files = 0
	m.budget = 0
}
func (m *Multipart) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("nextgo: multipart bodies are browser-local and cannot be serialized")
}
func (m *Multipart) formData() (js.Value, error) {
	if _, err := m.BudgetBytes(); err != nil {
		return js.Undefined(), err
	}
	body := js.Global().Get("FormData").New()
	for _, part := range m.parts {
		if part.file == nil {
			body.Call("append", part.name, part.text)
		} else {
			actual, err := browserFile(part.file.value)
			if err != nil || actual.info != part.file.info {
				return js.Undefined(), ErrFileRead
			}
			body.Call("append", part.name, part.file.value)
		}
	}
	return body, nil
}
