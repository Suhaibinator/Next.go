//go:build !js

// Package metadatafiles snapshots explicitly public app-root metadata. It does
// not serve application source, fetch resources or run per-request generators.
package metadatafiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/seo"
)

const MaxTotalBytes = 8 << 20
const MaxRobotsBytes = 512 << 10
const MaxSitemapBytes = 4 << 20
const MaxSitemapEntries = 4096
const MaxManifestBytes = 64 << 10
const MaxManifestIcons = 16
const MaxImageBytes = 1 << 20
const MaxImageDimension = 2048
const MaxImagePixels = 4 << 20

var names = []string{"robots.txt", "sitemap.xml", "favicon.ico", "icon.png", "apple-icon.png", "manifest.webmanifest"}

// Names returns the complete, fixed static filename convention.
func Names() []string { return append([]string(nil), names...) }
func IsName(name string) bool {
	for _, known := range names {
		if name == known {
			return true
		}
	}
	return false
}

// Sources are trusted startup inputs. Static is rooted at app/ (or at a subset
// containing the named files). Missing files are optional. Producers run once,
// with startup cancellation and no incoming request, and must cooperate with it.
// A static file and producer for the same resource conflict.
type Sources struct {
	Static   fs.FS
	Robots   func(context.Context) (seo.RobotsOptions, error)
	Sitemap  func(context.Context) ([]seo.SitemapEntry, error)
	Manifest func(context.Context) (Manifest, error)
}

type Options struct{ BasePath string }

// Entry describes one frozen public resource. Path includes only the global
// BasePath, never locale prefixes, asset origins or trailing-slash policy.
type Entry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
	ETag        string `json:"etag"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Sizes       string `json:"sizes,omitempty"`
}

type resource struct {
	entry Entry
	data  []byte
}

// Set is immutable after successful construction and safe for concurrent use.
// Its public methods return owned byte slices/descriptors rather than aliases.
type Set struct {
	entries []Entry
	byName  map[string]resource
	byPath  map[string]string
	digest  string
}

func New(ctx context.Context, sources Sources, options Options) (set *Set, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("metadatafiles: startup context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deployment := paths.Config{BasePath: options.BasePath}
	if err := deployment.Validate(); err != nil {
		return nil, fmt.Errorf("metadatafiles: invalid base path: %w", err)
	}
	// Startup URL helpers see the global application mount, with no locale,
	// asset-origin or trailing-slash reinterpretation of metadata resources.
	ctx = paths.WithContext(ctx, deployment)
	defer func() {
		if recovered := recover(); recovered != nil {
			set = nil
			err = fmt.Errorf("metadatafiles: startup producer panicked")
		}
	}()
	files, err := ReadStatic(ctx, sources.Static)
	if err != nil {
		return nil, err
	}
	// Resolve the complete source inventory before executing any producer.
	for _, source := range []struct {
		name    string
		present bool
	}{
		{"robots.txt", sources.Robots != nil},
		{"sitemap.xml", sources.Sitemap != nil},
		{"manifest.webmanifest", sources.Manifest != nil},
	} {
		if _, exists := files[source.name]; exists && source.present {
			return nil, fmt.Errorf("metadatafiles: static and Go definitions conflict for %s", source.name)
		}
	}
	put := func(name string, data []byte) error {
		if _, exists := files[name]; exists {
			return fmt.Errorf("metadatafiles: static and Go definitions conflict for %s", name)
		}
		if len(data) > fileLimit(name) {
			return fmt.Errorf("metadatafiles: producer output exceeds resource byte limit")
		}
		files[name] = append([]byte(nil), data...)
		return nil
	}
	if sources.Robots != nil {
		value, err := sources.Robots(ctx)
		if err != nil {
			return nil, fmt.Errorf("metadatafiles: robots producer: %w", err)
		}
		if err := robotInputBounds(value); err != nil {
			return nil, err
		}
		data, err := seo.MarshalRobots(value)
		if err != nil {
			return nil, err
		}
		if err := put("robots.txt", data); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sources.Sitemap != nil {
		value, err := sources.Sitemap(ctx)
		if err != nil {
			return nil, fmt.Errorf("metadatafiles: sitemap producer: %w", err)
		}
		if len(value) > MaxSitemapEntries {
			return nil, fmt.Errorf("metadatafiles: sitemap exceeds %d entries", MaxSitemapEntries)
		}
		urlBytes := 0
		for _, entry := range value {
			if len(entry.URL) > 8192 || len(entry.URL) > MaxSitemapBytes-urlBytes {
				return nil, fmt.Errorf("metadatafiles: sitemap producer URL text exceeds byte limits")
			}
			urlBytes += len(entry.URL)
		}
		data, err := seo.MarshalSitemap(value)
		if err != nil {
			return nil, err
		}
		if err := put("sitemap.xml", data); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sources.Manifest != nil {
		value, err := sources.Manifest(ctx)
		if err != nil {
			return nil, fmt.Errorf("metadatafiles: manifest producer: %w", err)
		}
		data, err := MarshalManifest(value, options)
		if err != nil {
			return nil, err
		}
		if err := put("manifest.webmanifest", data); err != nil {
			return nil, err
		}
	}
	set = &Set{byName: map[string]resource{}, byPath: map[string]string{}}
	total := 0
	for _, name := range names {
		data, ok := files[name]
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, err := Validate(name, data, options)
		if err != nil {
			return nil, err
		}
		if len(data) > MaxTotalBytes-total {
			return nil, fmt.Errorf("metadatafiles: resources exceed %d total bytes", MaxTotalBytes)
		}
		total += len(data)
		set.entries = append(set.entries, entry)
		set.byName[name] = resource{entry, append([]byte(nil), data...)}
		set.byPath[entry.Path] = name
	}
	h := sha256.New()
	for _, entry := range set.entries {
		fmt.Fprintf(h, "%d:%s:%d:%s:%d:%s:%d:%s;", len(entry.Name), entry.Name, len(entry.Path), entry.Path, len(entry.ContentType), entry.ContentType, len(entry.Digest), entry.Digest)
	}
	if len(set.entries) > 0 {
		set.digest = hex.EncodeToString(h.Sum(nil))
	}
	return set, nil
}

func (s *Set) Entries() []Entry {
	if s == nil {
		return nil
	}
	return append([]Entry(nil), s.entries...)
}
func (s *Set) Digest() string {
	if s == nil {
		return ""
	}
	return s.digest
}
func (s *Set) Read(name string) ([]byte, error) {
	if s == nil {
		return nil, fs.ErrNotExist
	}
	resource, ok := s.byName[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), resource.data...), nil
}
func (s *Set) Handles(escapedPath string) bool {
	if s == nil {
		return false
	}
	_, ok := s.byPath[escapedPath]
	return ok
}

// Head returns final global mounted links. Append these after normal page URL
// resolution; applying a locale/BasePath resolver again would double-prefix.
// The framework's page metadata controls decide whether defaults are overridden.
func (s *Set) Head() []seo.Element {
	if s == nil {
		return nil
	}
	var head []seo.Element
	for _, name := range []string{"favicon.ico", "icon.png", "apple-icon.png", "manifest.webmanifest"} {
		resource, ok := s.byName[name]
		if !ok {
			continue
		}
		entry := resource.entry
		rel := "icon"
		if name == "apple-icon.png" {
			rel = "apple-touch-icon"
		}
		if name == "manifest.webmanifest" {
			rel = "manifest"
		}
		attrs := map[string]string{"rel": rel, "href": entry.Path, "type": entry.ContentType}
		if entry.Sizes != "" {
			attrs["sizes"] = entry.Sizes
		}
		head = append(head, seo.Element{Tag: "link", Attrs: maps.Clone(attrs)})
	}
	return head
}

// Validate checks one allowed file without executing a producer or accessing the
// filesystem. Options is optional for discovery; startup supplies its real base.
func Validate(name string, data []byte, options ...Options) (Entry, error) {
	entry := Entry{Name: name}
	if !IsName(name) {
		return entry, fmt.Errorf("metadatafiles: unsupported filename %q", name)
	}
	if len(options) > 1 {
		return entry, fmt.Errorf("metadatafiles: at most one options value")
	}
	var opts Options
	if len(options) == 1 {
		opts = options[0]
	}
	deployment := paths.Config{BasePath: strings.Clone(opts.BasePath)}
	if err := deployment.Validate(); err != nil {
		return entry, err
	}
	var err error
	switch name {
	case "robots.txt":
		entry.ContentType = "text/plain; charset=utf-8"
		err = validateRobots(data)
	case "sitemap.xml":
		entry.ContentType = "application/xml; charset=utf-8"
		err = validateSitemap(data)
	case "manifest.webmanifest":
		entry.ContentType = "application/manifest+json"
		_, err = ParseManifest(data, opts)
	case "icon.png", "apple-icon.png":
		entry.ContentType = "image/png"
		entry.Width, entry.Height, err = validatePNG(data)
		if err == nil {
			entry.Sizes = strconv.Itoa(entry.Width) + "x" + strconv.Itoa(entry.Height)
		}
	case "favicon.ico":
		entry.ContentType = "image/x-icon"
		entry.Width, entry.Height, entry.Sizes, err = validateICO(data)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("metadatafiles: %s: %w", name, err)
	}
	entry.Path, err = deployment.URL("/" + name)
	if err != nil {
		return Entry{}, err
	}
	entry.Size = int64(len(data))
	sum := sha256.Sum256(data)
	entry.Digest = hex.EncodeToString(sum[:])
	entry.ETag = `"` + entry.Digest + `"`
	return entry, nil
}

// CheckCollisions rejects exact paths and handler prefixes that shadow a frozen
// resource. Callers supply logical or mounted declarations as appropriate.
func (s *Set) CheckCollisions(paths []string) error {
	if s == nil {
		return nil
	}
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	for _, path := range paths {
		for _, entry := range s.entries {
			logical := "/" + entry.Name
			if path == entry.Path || path == logical || strings.HasSuffix(path, "/") && (strings.HasPrefix(entry.Path, path) || strings.HasPrefix(logical, path)) {
				return fmt.Errorf("metadatafiles: resource %s conflicts with route or handler %s", entry.Name, path)
			}
		}
	}
	return nil
}
