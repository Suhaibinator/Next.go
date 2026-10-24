// Package httpx contains optional HTTP transport middleware.
package httpx

import (
	"bufio"
	"compress/gzip"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Compression negotiates streaming gzip responses. It merges Vary:
// Accept-Encoding, respects explicit encoding quality preferences, and avoids
// transforming HEAD/range/upgrade responses, existing encodings, no-transform
// responses, bodyless statuses, and media types already normally compressed.
// No complete response is buffered. Headers are committed on the first write
// or flush, allowing an empty response to remain unencoded.
//
// http.Flusher and http.Hijacker are exposed only when the underlying writer
// supports them. Unwrap supports http.ResponseController. A flush also flushes
// gzip, so server-sent events and streamed HTML remain incremental.
func Compression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addVary(w.Header(), "Accept-Encoding")
		gzipOK, identityOK := negotiate(r.Header.Values("Accept-Encoding"))
		if !gzipOK && !identityOK {
			http.Error(w, "No acceptable content encoding", http.StatusNotAcceptable)
			return
		}
		if !gzipOK || r.Method == http.MethodHead || r.Header.Get("Range") != "" || r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		response := &responseWriter{underlying: w, headers: w.Header().Clone()}
		// Reclaim compressor memory even if the handler or transport panics.
		// Releasing does not close/commit a partially written HTTP response.
		defer response.releaseWriter()
		var wrapped http.ResponseWriter = response
		_, flush := w.(http.Flusher)
		_, hijack := w.(http.Hijacker)
		switch {
		case flush && hijack:
			wrapped = &flushHijackWriter{response}
		case flush:
			wrapped = &flushWriter{response}
		case hijack:
			wrapped = &hijackWriter{response}
		}
		next.ServeHTTP(wrapped, r)
		response.finish()
	})
}

type responseWriter struct {
	underlying http.ResponseWriter
	headers    http.Header
	frozen     http.Header
	status     int
	committed  bool
	hijacked   bool
	zip        *gzip.Writer
}

func (w *responseWriter) Header() http.Header { return w.headers }

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.underlying }

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 || w.committed || w.hijacked {
		return
	}
	if status < 100 || status > 999 {
		panic("httpx: invalid HTTP status code " + strconv.Itoa(status))
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		copyHeaders(w.underlying.Header(), w.headers)
		w.underlying.WriteHeader(status)
		return
	}
	w.status = status
	w.frozen = w.headers.Clone()
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.hijacked {
		return 0, http.ErrHijacked
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !bodyAllowed(w.status) {
		w.commit(nil, false)
		return w.underlying.Write(body)
	}
	if len(body) == 0 {
		return 0, nil
	}
	w.commit(body, true)
	if w.zip != nil {
		return w.zip.Write(body)
	}
	return w.underlying.Write(body)
}

func (w *responseWriter) commit(sample []byte, encode bool) {
	if w.committed || w.hijacked {
		return
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	h := w.frozen
	addVary(h, "Accept-Encoding")
	if len(sample) > 0 && h.Get("Content-Type") == "" && h.Get("Content-Encoding") == "" {
		h.Set("Content-Type", http.DetectContentType(sample))
	}
	if encode && bodyAllowed(w.status) && shouldEncode(h) {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		h.Del("Content-MD5")
		h.Del("Digest")
		h.Del("Content-Digest")
		h.Del("Accept-Ranges")
		if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
			h.Set("ETag", "W/"+etag)
		}
		w.zip = compressors.get(w.underlying)
	}
	copyHeaders(w.underlying.Header(), h)
	w.underlying.WriteHeader(w.status)
	w.committed = true
}

func (w *responseWriter) flush() {
	if w.hijacked {
		return
	}
	w.commit(nil, true)
	if w.zip != nil {
		_ = w.zip.Flush()
	}
	w.underlying.(http.Flusher).Flush()
}

func (w *responseWriter) finish() {
	if w.hijacked {
		return
	}
	w.commit(nil, false)
	if w.zip != nil {
		_ = w.zip.Close()
	}
	// Headers added after WriteHeader are ignored except declared trailers and
	// the net/http TrailerPrefix convention.
	for _, declaration := range w.frozen.Values("Trailer") {
		for _, name := range strings.Split(declaration, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			w.underlying.Header()[name] = append([]string(nil), w.headers.Values(name)...)
		}
	}
	for name, values := range w.headers {
		if strings.HasPrefix(name, http.TrailerPrefix) {
			w.underlying.Header()[name] = append([]string(nil), values...)
		}
	}
}

func (w *responseWriter) releaseWriter() {
	if w.zip != nil {
		writer := w.zip
		w.zip = nil
		compressors.put(writer)
	}
}

// Limit retained idle compressors independently of peak request concurrency.
// A compressor's flate workspace is substantial; an unbounded pool would turn
// a traffic burst into a persistent memory increase. Active requests each own
// their writer and are governed by the server's request concurrency limits.
const maxIdleGzipWriters = 16

type gzipWriterPool chan *gzip.Writer

var compressors = make(gzipWriterPool, maxIdleGzipWriters)

func (pool gzipWriterPool) get(destination io.Writer) *gzip.Writer {
	select {
	case writer := <-pool:
		writer.Reset(destination)
		return writer
	default:
		return gzip.NewWriter(destination)
	}
}

func (pool gzipWriterPool) put(writer *gzip.Writer) {
	// Reset on every release, including when the pool is full or a response
	// ended in an error/panic/hijack. Never retain a request's ResponseWriter,
	// connection, headers, or output buffer through a pooled compressor.
	writer.Reset(io.Discard)
	select {
	case pool <- writer:
	default:
	}
}

func (w *responseWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := w.underlying.(http.Hijacker).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, buffer, err
}

type flushWriter struct{ *responseWriter }

func (w *flushWriter) Flush() { w.flush() }

type hijackWriter struct{ *responseWriter }

func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

type flushHijackWriter struct{ *responseWriter }

func (w *flushHijackWriter) Flush()                                       { w.flush() }
func (w *flushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusResetContent && status != http.StatusNotModified
}

func shouldEncode(h http.Header) bool {
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	for _, line := range h.Values("Cache-Control") {
		for _, directive := range strings.Split(line, ",") {
			name, _, _ := strings.Cut(directive, "=")
			if strings.EqualFold(strings.TrimSpace(name), "no-transform") {
				return false
			}
		}
	}
	contentType, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		return true
	}
	if strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml" || strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") {
		return false
	}
	switch contentType {
	case "application/zip", "application/gzip", "application/x-gzip", "application/x-7z-compressed", "application/vnd.rar", "application/pdf":
		return false
	}
	return true
}

func addVary(h http.Header, field string) {
	for _, line := range h.Values("Vary") {
		for _, token := range strings.Split(line, ",") {
			if token = strings.TrimSpace(token); token == "*" || strings.EqualFold(token, field) {
				return
			}
		}
	}
	h.Add("Vary", field)
}

func copyHeaders(dst, src http.Header) {
	for name := range dst {
		delete(dst, name)
	}
	for name, values := range src {
		dst[name] = append([]string(nil), values...)
	}
}

// An explicit identity preference outranks gzip only when its quality is
// higher. Unlisted identity remains acceptable without expressing a preference.
func negotiate(lines []string) (gzipOK, identityOK bool) {
	gzipQ, identityQ, wildcardQ := -1, -1, -1
	for _, line := range lines {
		for _, item := range strings.Split(line, ",") {
			parts := strings.Split(item, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			quality := 1000
			for _, parameter := range parts[1:] {
				key, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
				if strings.EqualFold(key, "q") {
					if !found {
						quality = 0
					} else {
						quality = parseQuality(value)
					}
				}
			}
			switch name {
			case "gzip":
				if quality > gzipQ {
					gzipQ = quality
				}
			case "identity":
				if quality > identityQ {
					identityQ = quality
				}
			case "*":
				if quality > wildcardQ {
					wildcardQ = quality
				}
			}
		}
	}
	if gzipQ < 0 {
		gzipQ = wildcardQ
	}
	identityOK = identityQ != 0 && !(identityQ < 0 && wildcardQ == 0)
	gzipOK = gzipQ > 0 && (identityQ < 0 || gzipQ >= identityQ)
	return gzipOK, identityOK
}

func parseQuality(value string) int {
	value = strings.TrimSpace(value)
	whole, fraction, hasFraction := strings.Cut(value, ".")
	if whole != "0" && whole != "1" || len(fraction) > 3 {
		return 0
	}
	q := 0
	for _, digit := range fraction {
		if digit < '0' || digit > '9' || whole == "1" && digit != '0' {
			return 0
		}
		q = q*10 + int(digit-'0')
	}
	if whole == "1" {
		return 1000
	}
	if !hasFraction {
		return 0
	}
	for i := len(fraction); i < 3; i++ {
		q *= 10
	}
	return q
}
