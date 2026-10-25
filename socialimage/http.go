//go:build !js

package socialimage

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Builders can wrap these errors to select generic 400/404 HTTP responses. Other
// builder, layout, decoder and font errors produce a generic 500 with no-store.
var (
	ErrBadRequest = errors.New("socialimage: bad request")
	ErrNotFound   = errors.New("socialimage: not found")
)

// Builder executes for every admitted GET/HEAD, including cache hits and ETag
// validation. Perform authorization here before returning any private content.
// It must honor ctx; Go cannot forcibly interrupt an application callback.
type Builder func(ctx context.Context, r *http.Request) (Document, error)

type Config struct {
	Limits          Limits
	MaxConcurrent   int
	MaxCacheBytes   int
	MaxCacheEntries int
	MaxRequestBytes int
	DisableCache    bool
	// Public explicitly declares all successful responses safe for shared HTTP
	// caches. The default uses private, max-age=0, must-revalidate. Never enable
	// Public for user-specific, authorization-dependent, or secret content.
	Public bool
}

type encoded struct {
	key, etag string
	data      []byte
}

// Handler bounds concurrent builders, planning, decodes, and encoding together.
// A canceled noncooperative builder retains its slot until it actually returns.
// The process-local LRU stores encoded bytes only, by validated drawing content.
type Handler struct {
	c          Config
	build      Builder
	workers    chan struct{}
	mu         sync.Mutex
	entries    map[string]*list.Element
	lru        *list.List
	cacheBytes int
}

func NewHandler(c Config, build Builder) (*Handler, error) {
	var err error
	c.Limits, err = c.Limits.normalized()
	if err != nil {
		return nil, err
	}
	if build == nil {
		return nil, fmt.Errorf("%w: nil builder", ErrInvalid)
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 2
	}
	if c.MaxCacheBytes == 0 {
		c.MaxCacheBytes = 16 << 20
	}
	if c.MaxCacheEntries == 0 {
		c.MaxCacheEntries = 64
	}
	if c.MaxRequestBytes == 0 {
		c.MaxRequestBytes = 8192
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 64 || c.MaxCacheBytes < 1 || c.MaxCacheBytes > 256<<20 || c.MaxCacheEntries < 1 || c.MaxCacheEntries > 4096 || c.MaxRequestBytes < 1 || c.MaxRequestBytes > 64<<10 {
		return nil, fmt.Errorf("%w: handler limits", ErrInvalid)
	}
	return &Handler{c: c, build: build, workers: make(chan struct{}, c.MaxConcurrent), entries: make(map[string]*list.Element), lru: list.New()}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		h.fail(w, r, http.StatusMethodNotAllowed)
		return
	}
	if len(r.URL.Path) > h.c.MaxRequestBytes || len(r.URL.RawQuery) > h.c.MaxRequestBytes-len(r.URL.Path) {
		h.fail(w, r, http.StatusRequestURITooLong)
		return
	}
	select {
	case h.workers <- struct{}{}:
		defer func() { <-h.workers }()
	case <-r.Context().Done():
		return
	}
	if r.Context().Err() != nil {
		return
	}
	doc, err := h.build(r.Context(), r)
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrBadRequest) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, ErrNotFound) {
			status = http.StatusNotFound
		}
		h.fail(w, r, status)
		return
	}
	p, err := prepare(r.Context(), doc, h.c.Limits)
	if err != nil {
		if r.Context().Err() == nil {
			h.fail(w, r, http.StatusInternalServerError)
		}
		return
	}
	key, err := p.key(r.Context())
	if err != nil {
		if r.Context().Err() == nil {
			h.fail(w, r, http.StatusInternalServerError)
		}
		return
	}
	entry := h.get(key)
	if entry == nil {
		data, err := p.render(r.Context(), h.c.Limits)
		if err != nil {
			if r.Context().Err() == nil {
				h.fail(w, r, http.StatusInternalServerError)
			}
			return
		}
		digest := sha256.Sum256(data)
		entry = &encoded{key: key, etag: `"` + hex.EncodeToString(digest[:]) + `"`, data: data}
		if r.Context().Err() != nil {
			return
		}
		h.put(entry)
	}
	if r.Context().Err() != nil {
		return
	}
	h.serve(w, r, entry)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = fmt.Fprintln(w, http.StatusText(status))
	}
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, v *encoded) {
	w.Header().Set("Content-Type", "image/png")
	policy := "private, max-age=0, must-revalidate"
	if h.c.Public {
		policy = "public, max-age=0, must-revalidate"
	}
	w.Header().Set("Cache-Control", policy)
	w.Header().Set("ETag", v.etag)
	for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == v.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(v.data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(v.data)
	}
}

func (h *Handler) get(key string) *encoded {
	if h.c.DisableCache {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.entries[key]; e != nil {
		h.lru.MoveToFront(e)
		return e.Value.(*encoded)
	}
	return nil
}

func (h *Handler) put(v *encoded) {
	if h.c.DisableCache || len(v.data) > h.c.MaxCacheBytes {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.entries[v.key]; e != nil {
		h.lru.MoveToFront(e)
		return
	}
	h.entries[v.key] = h.lru.PushFront(v)
	h.cacheBytes += len(v.data)
	for h.lru.Len() > h.c.MaxCacheEntries || h.cacheBytes > h.c.MaxCacheBytes {
		e := h.lru.Back()
		old := e.Value.(*encoded)
		delete(h.entries, old.key)
		h.cacheBytes -= len(old.data)
		h.lru.Remove(e)
	}
}
