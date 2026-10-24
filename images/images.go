// Package images optimizes images from a local fs.FS and renders responsive HTML.
// It intentionally never fetches remote URLs. Supported input formats are JPEG,
// PNG, and GIF (first frame); output is JPEG or PNG, not WebP or AVIF.
package images

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Suhaibinator/Next.go/ui"
)

// Config bounds memory, decoding work, and the number of possible resize widths.
// FS must expose only public images; an os.DirFS must not contain symlinks that
// escape its root. Endpoint must match the HTTP path where Handler is mounted.
// Zero limits select conservative defaults. Source bytes are re-read so edits
// invalidate the cache even when a file's modification time is unchanged.
type Config struct {
	FS              fs.FS
	Endpoint        string
	Widths          []int
	MaxInputBytes   int64
	MaxPixels       int64
	MaxDimension    int
	MaxCacheBytes   int64
	MaxCacheEntries int
	MaxConcurrent   int
}

type cached struct {
	key, contentType, etag string
	data                   []byte
}

// Handler is safe for concurrent use. Its cache holds at most the configured
// encoded byte count and entry count; source/decoded images are never cached.
type Handler struct {
	c          Config
	mu         sync.Mutex
	lru        *list.List
	entries    map[string]*list.Element
	cacheBytes int64
	workers    chan struct{}
}

func New(c Config) (*Handler, error) {
	if c.FS == nil {
		return nil, errors.New("images: FS is required")
	}
	if c.Endpoint == "" {
		c.Endpoint = "/_nextgo/image"
	}
	if !localPath(c.Endpoint) || strings.ContainsAny(c.Endpoint, "?#,% ") {
		return nil, errors.New("images: Endpoint must be a local absolute path")
	}
	if c.MaxInputBytes == 0 {
		c.MaxInputBytes = 10 << 20
	}
	if c.MaxPixels == 0 {
		c.MaxPixels = 16_000_000
	}
	if c.MaxDimension == 0 {
		c.MaxDimension = 4096
	}
	if c.MaxCacheBytes == 0 {
		c.MaxCacheBytes = 32 << 20
	}
	if c.MaxCacheEntries == 0 {
		c.MaxCacheEntries = 128
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 2
	}
	if c.MaxInputBytes < 1 || c.MaxInputBytes > 1<<30 || c.MaxPixels < 1 || c.MaxPixels > 100_000_000 || c.MaxDimension < 1 || c.MaxDimension > 16384 || c.MaxCacheBytes < 1 || c.MaxCacheEntries < 1 || c.MaxConcurrent < 1 || c.MaxConcurrent > 64 {
		return nil, errors.New("images: invalid resource limits")
	}
	if len(c.Widths) == 0 {
		c.Widths = []int{320, 640, 768, 1024, 1280, 1536, 2048}
	}
	c.Widths = slices.Clone(c.Widths)
	slices.Sort(c.Widths)
	c.Widths = slices.Compact(c.Widths)
	if len(c.Widths) > 64 {
		return nil, errors.New("images: at most 64 widths are allowed")
	}
	for _, w := range c.Widths {
		if w < 1 || w > c.MaxDimension {
			return nil, errors.New("images: widths must be positive and within MaxDimension")
		}
	}
	return &Handler{c: c, lru: list.New(), entries: map[string]*list.Element{}, workers: make(chan struct{}, c.MaxConcurrent)}, nil
}

func localPath(src string) bool {
	return strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "//") && !strings.ContainsAny(src, "\\\x00\r\n") && fs.ValidPath(strings.TrimPrefix(src, "/"))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query", 400)
		return
	}
	for key, values := range q {
		if (key != "src" && key != "w" && key != "q") || len(values) != 1 {
			http.Error(w, "invalid query", 400)
			return
		}
	}
	src := q.Get("src")
	width, err := strconv.Atoi(q.Get("w"))
	if !localPath(src) || strings.ContainsAny(src, "?#%") || err != nil || !slices.Contains(h.c.Widths, width) {
		http.Error(w, "invalid source or width", 400)
		return
	}
	quality := 75
	if v, ok := q["q"]; ok {
		quality, err = strconv.Atoi(v[0])
		if err != nil || quality < 1 || quality > 100 {
			http.Error(w, "invalid quality", 400)
			return
		}
	}
	// Bound all in-flight source reads as well as decodes. Waiting requests can
	// be canceled without occupying a transformation slot.
	select {
	case h.workers <- struct{}{}:
		defer func() { <-h.workers }()
	case <-r.Context().Done():
		return
	}
	if r.Context().Err() != nil {
		return
	}
	f, err := h.c.FS.Open(strings.TrimPrefix(src, "/"))
	if err != nil {
		http.Error(w, "image not found", 404)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "image not found", 404)
		return
	}
	if info.Size() > h.c.MaxInputBytes {
		http.Error(w, "image too large", 413)
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, h.c.MaxInputBytes+1))
	if err != nil {
		http.Error(w, "cannot read image", 500)
		return
	}
	if int64(len(data)) > h.c.MaxInputBytes {
		http.Error(w, "image too large", 413)
		return
	}
	digest := sha256.Sum256(data)
	key := fmt.Sprintf("%x:%d:%d", digest, width, quality)
	if hit := h.get(key); hit != nil {
		h.serve(w, r, hit)
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png" && format != "gif") {
		http.Error(w, "unsupported or invalid image", 415)
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > h.c.MaxPixels/int64(cfg.Height) {
		http.Error(w, "image dimensions too large", 413)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		http.Error(w, "invalid image", 415)
		return
	}
	if err := r.Context().Err(); err != nil {
		return
	}
	outWidth, outHeight := dimensions(cfg.Width, cfg.Height, width, h.c.MaxDimension)
	if outWidth != cfg.Width || outHeight != cfg.Height {
		im = resize(im, outWidth, outHeight)
	}
	var encoded bytes.Buffer
	contentType := "image/png"
	if format == "jpeg" {
		contentType = "image/jpeg"
		err = jpeg.Encode(&encoded, im, &jpeg.Options{Quality: quality})
	} else {
		err = png.Encode(&encoded, im)
	}
	if err != nil {
		http.Error(w, "cannot encode image", 500)
		return
	}
	output := encoded.Bytes()
	outputHash := sha256.Sum256(output)
	entry := &cached{key: key, contentType: contentType, etag: `"` + hex.EncodeToString(outputHash[:]) + `"`, data: output}
	h.put(entry)
	h.serve(w, r, entry)
}

func dimensions(w, h, requested, max int) (int, int) {
	scale := math.Min(1, math.Min(float64(requested)/float64(w), float64(max)/float64(h)))
	return int(math.Max(1, math.Floor(float64(w)*scale))), int(math.Max(1, math.Floor(float64(h)*scale)))
}

// resize uses bilinear sampling of premultiplied colors, preserving PNG alpha.
func resize(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	for y := 0; y < h; y++ {
		sy := (float64(y)+0.5)*float64(b.Dy())/float64(h) - 0.5
		if sy < 0 {
			sy = 0
		}
		y0 := int(sy)
		y1 := min(y0+1, b.Dy()-1)
		fy := sy - float64(y0)
		for x := 0; x < w; x++ {
			sx := (float64(x)+0.5)*float64(b.Dx())/float64(w) - 0.5
			if sx < 0 {
				sx = 0
			}
			x0 := int(sx)
			x1 := min(x0+1, b.Dx()-1)
			fx := sx - float64(x0)
			r0, g0, b0, a0 := src.At(b.Min.X+x0, b.Min.Y+y0).RGBA()
			r1, g1, b1, a1 := src.At(b.Min.X+x1, b.Min.Y+y0).RGBA()
			r2, g2, b2, a2 := src.At(b.Min.X+x0, b.Min.Y+y1).RGBA()
			r3, g3, b3, a3 := src.At(b.Min.X+x1, b.Min.Y+y1).RGBA()
			blend := func(a, b, c, d uint32) uint8 {
				return uint8(((float64(a)*(1-fx)+float64(b)*fx)*(1-fy)+(float64(c)*(1-fx)+float64(d)*fx)*fy)/257 + 0.5)
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i] = blend(r0, r1, r2, r3)
			dst.Pix[i+1] = blend(g0, g1, g2, g3)
			dst.Pix[i+2] = blend(b0, b1, b2, b3)
			dst.Pix[i+3] = blend(a0, a1, a2, a3)
		}
	}
	return dst
}

func (h *Handler) get(key string) *cached {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.entries[key]; e != nil {
		h.lru.MoveToFront(e)
		return e.Value.(*cached)
	}
	return nil
}
func (h *Handler) put(v *cached) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if int64(len(v.data)) > h.c.MaxCacheBytes {
		return
	}
	if existing := h.entries[v.key]; existing != nil {
		h.lru.MoveToFront(existing)
		return
	}
	h.entries[v.key] = h.lru.PushFront(v)
	h.cacheBytes += int64(len(v.data))
	for h.lru.Len() > h.c.MaxCacheEntries || h.cacheBytes > h.c.MaxCacheBytes {
		e := h.lru.Back()
		old := e.Value.(*cached)
		delete(h.entries, old.key)
		h.cacheBytes -= int64(len(old.data))
		h.lru.Remove(e)
	}
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, v *cached) {
	w.Header().Set("Content-Type", v.contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
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

// ImageOptions defines intrinsic dimensions to prevent layout shifts. Quality
// affects JPEG only. Sizes defaults to 100vw; Priority opts into eager loading.
type ImageOptions struct {
	// Endpoint overrides only the rendered local URL, e.g. a deployment base.
	Endpoint      string
	Src, Alt      string
	Width, Height int
	Sizes, Class  string
	Quality       int
	Priority      bool
}

type errorNode struct{ err error }

func (e errorNode) Render(io.Writer) error { return e.err }

// Image renders a responsive img with local optimized variants. It returns a
// rendering error for invalid input. Props and paths must refer to public data.
func (h *Handler) Image(o ImageOptions) ui.Node {
	if !localPath(o.Src) || strings.ContainsAny(o.Src, "?#%") || o.Width < 1 || o.Height < 1 {
		return errorNode{errors.New("images: Image requires a local source and positive intrinsic dimensions")}
	}
	if o.Quality == 0 {
		o.Quality = 75
	}
	if o.Quality < 1 || o.Quality > 100 {
		return errorNode{errors.New("images: invalid quality")}
	}
	endpoint := o.Endpoint
	if endpoint == "" {
		endpoint = h.c.Endpoint
	}
	if !localPath(endpoint) || strings.ContainsAny(endpoint, "?#,% ") {
		return errorNode{errors.New("images: invalid rendered endpoint")}
	}
	variant := func(width int) string {
		return endpoint + "?" + url.Values{"src": {o.Src}, "w": {strconv.Itoa(width)}, "q": {strconv.Itoa(o.Quality)}}.Encode()
	}
	fallback := h.c.Widths[len(h.c.Widths)-1]
	for _, width := range h.c.Widths {
		if width >= o.Width {
			fallback = width
			break
		}
	}
	var candidates []string
	for _, width := range h.c.Widths {
		if width > o.Width {
			break
		}
		actual, _ := dimensions(o.Width, o.Height, width, h.c.MaxDimension)
		// Very tall images can hit the output height bound first. Exclude
		// candidates whose encoded width would differ from their descriptor.
		if actual == width {
			candidates = append(candidates, variant(width)+" "+strconv.Itoa(width)+"w")
		}
	}
	// Never label an upscaled response with a larger width descriptor.
	attrs := ui.Attrs{"src": variant(fallback), "alt": o.Alt, "width": strconv.Itoa(o.Width), "height": strconv.Itoa(o.Height), "loading": "lazy", "decoding": "async"}
	if len(candidates) > 0 {
		attrs["srcset"] = strings.Join(candidates, ", ")
		attrs["sizes"] = o.Sizes
		if o.Sizes == "" {
			attrs["sizes"] = "100vw"
		}
	}
	if o.Class != "" {
		attrs["class"] = o.Class
	}
	if o.Priority {
		attrs["loading"] = "eager"
		attrs["fetchpriority"] = "high"
	}
	return ui.El("img", attrs)
}
