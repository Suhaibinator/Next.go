package ui

import (
	"context"
	"io"
	"unicode/utf8"
)

// htmlUTF8Writer canonicalizes a framework render unit before its output reaches
// a buffer/quota or transport. It retains at most three incomplete UTF-8 bytes,
// preserves valid bytes (including NUL), and replaces each malformed byte with
// U+FFFD, matching encoding/json's string policy. It never buffers a whole unit.
type htmlUTF8Writer struct {
	ctx     context.Context
	dst     io.Writer
	pending [utf8.UTFMax - 1]byte
	count   int
	err     error
}

func (w *htmlUTF8Writer) canonicalHTML() bool { return true }

func canonicalHTMLWriter(w io.Writer) bool {
	value, ok := w.(interface{ canonicalHTML() bool })
	return ok && value.canonicalHTML()
}

// Preserve the existing concrete stream/capture writer passed to custom Nodes.
// Their embedded context writer forwards this private capability so a nested
// RenderTo does not normalize an incomplete rune a second time.
func (w *contextWriter) canonicalHTML() bool { return canonicalHTMLWriter(w.w) }

func (w *htmlUTF8Writer) record(n, size int, err error) (int, error) {
	if n < 0 || n > size {
		n, err = 0, io.ErrShortWrite
	} else if n != size && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

func (w *htmlUTF8Writer) output(value []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	n, err := w.dst.Write(value)
	return w.record(n, len(value), err)
}

func (w *htmlUTF8Writer) outputString(value string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	if sink, ok := w.dst.(io.StringWriter); ok {
		n, err := sink.WriteString(value)
		return w.record(n, len(value), err)
	}
	// io.WriteString's fallback copies its entire string. Keep our fallback
	// bounded for arbitrary caller writers, while buffers use their fast path.
	var scratch [1024]byte
	used := 0
	for used < len(value) {
		size := copy(scratch[:], value[used:])
		n, err := w.output(scratch[:size])
		used += n
		if err != nil {
			return used, err
		}
	}
	return used, nil
}

func outputHTML[T string | []byte](w *htmlUTF8Writer, value T) (int, error) {
	switch value := any(value).(type) {
	case string:
		return w.outputString(value)
	case []byte:
		return w.output(value)
	}
	panic("ui: unreachable HTML input type")
}

func writeHTML[T string | []byte](w *htmlUTF8Writer, value T) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	used := 0
	for used < len(value) || w.count > 0 {
		if w.count > 0 {
			var look [utf8.UTFMax]byte
			size := copy(look[:], w.pending[:w.count])
			for offset := used; size < len(look) && offset < len(value); offset++ {
				look[size] = value[offset]
				size++
			}
			if !utf8.FullRune(look[:size]) {
				// An incomplete UTF-8 prefix is always shorter than four bytes.
				w.count = copy(w.pending[:], look[:size])
				return len(value), nil
			}
			r, width := utf8.DecodeRune(look[:size])
			if r == utf8.RuneError && width == 1 {
				n, err := w.outputString("\uFFFD")
				if n == len("\uFFFD") {
					copy(w.pending[:], w.pending[1:w.count])
					w.count--
				}
				if err != nil {
					return used, err
				}
				continue
			}
			previous := w.count
			n, err := w.output(look[:width])
			// Earlier calls already accepted the pending prefix. Count only
			// bytes from this call that the underlying writer received.
			if n > previous {
				used += n - previous
			}
			if n == width {
				w.count = 0
			}
			if err != nil {
				return used, err
			}
			continue
		}
		end := used
		for end < len(value) {
			if value[end] < utf8.RuneSelf {
				end++
				continue
			}
			// At most four bytes are converted for the shared string/byte path.
			r, width := utf8.DecodeRuneInString(string(value[end:min(end+utf8.UTFMax, len(value))]))
			if r == utf8.RuneError && width == 1 {
				break
			}
			end += width
		}
		if end > used {
			n, err := outputHTML(w, value[used:end])
			used += n
			if err != nil {
				return used, err
			}
			continue
		}
		if !utf8.FullRuneInString(string(value[used:min(used+utf8.UTFMax, len(value))])) {
			for offset := used; offset < len(value); offset++ {
				w.pending[w.count] = value[offset]
				w.count++
			}
			return len(value), nil
		}
		n, err := w.outputString("\uFFFD")
		if n == len("\uFFFD") {
			used++
		}
		if err != nil {
			return used, err
		}
	}
	return used, nil
}

func (w *htmlUTF8Writer) Write(value []byte) (int, error)       { return writeHTML(w, value) }
func (w *htmlUTF8Writer) WriteString(value string) (int, error) { return writeHTML(w, value) }

// finish ends a render unit (or an ASCII-framed region boundary). It does not
// close the destination. An incomplete final sequence gets one U+FFFD per byte.
func (w *htmlUTF8Writer) finish() error {
	if w.err != nil {
		return w.err
	}
	for w.count > 0 {
		n, err := w.outputString("\uFFFD")
		if n == len("\uFFFD") {
			copy(w.pending[:], w.pending[1:w.count])
			w.count--
		}
		if err != nil {
			return err
		}
	}
	return nil
}
