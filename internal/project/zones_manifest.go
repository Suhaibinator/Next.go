package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const maxZonesManifestBytes = 1 << 20

type ZoneManifest struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	BasePath string `json:"basePath"`
	Tenant   string `json:"tenant,omitempty"`
	App      string `json:"app"`
	Root     string `json:"-"`
}

type ZonesManifest struct {
	Version int            `json:"version"`
	Zones   []ZoneManifest `json:"zones"`
	Path    string         `json:"-"`
	Root    string         `json:"-"`
	raw     []byte
}

func readZonesManifest(name string) ([]byte, string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, "", err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, abs, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxZonesManifestBytes {
		return nil, abs, fmt.Errorf("zones manifest must be a regular file of at most 1 MiB")
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, abs, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, abs, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, abs, fmt.Errorf("zones manifest changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxZonesManifestBytes+1))
	if err != nil {
		return nil, abs, err
	}
	if len(data) > maxZonesManifestBytes {
		return nil, abs, fmt.Errorf("zones manifest exceeds 1 MiB")
	}
	return data, abs, nil
}

func validZoneManifestID(value string, limit int, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > limit {
		return false
	}
	for i, c := range []byte(value) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// DiscoverZones reads build declarations without running application code. App
// directories may still be missing so development can watch their creation.
func DiscoverZones(name string) (*ZonesManifest, error) {
	data, path, err := readZonesManifest(name)
	if err != nil {
		return nil, err
	}
	return parseZonesManifest(data, path)
}

func parseZonesManifest(data []byte, path string) (*ZonesManifest, error) {
	var manifest ZonesManifest
	if err := wirejson.Unique(data); err != nil {
		return nil, fmt.Errorf("zones manifest: %w", err)
	}
	if err := wirejson.Strict(data, &manifest); err != nil {
		return nil, fmt.Errorf("zones manifest: %w", err)
	}
	if manifest.Version != 1 || len(manifest.Zones) == 0 || len(manifest.Zones) > 64 {
		return nil, fmt.Errorf("zones manifest requires version 1 and 1 to 64 zones")
	}
	manifest.Path, manifest.Root, manifest.raw = path, filepath.Dir(path), data
	ids, bindings := map[string]bool{}, map[string]bool{}
	for i := range manifest.Zones {
		zone := &manifest.Zones[i]
		if !validZoneManifestID(zone.ID, 64, false) || !validZoneManifestID(zone.Tenant, 128, true) {
			return nil, fmt.Errorf("zone %d: invalid ID or tenant", i)
		}
		key := strings.ToLower(zone.ID)
		if ids[key] {
			return nil, fmt.Errorf("duplicate or case-colliding zone ID %q", zone.ID)
		}
		ids[key] = true
		host, err := paths.CanonicalHost(zone.Host)
		if err != nil {
			return nil, fmt.Errorf("zone %q: invalid exact host", zone.ID)
		}
		zone.Host = host
		if err := (paths.Config{BasePath: zone.BasePath}).Validate(); err != nil {
			return nil, fmt.Errorf("zone %q: %w", zone.ID, err)
		}
		binding := host + "\x00" + zone.BasePath
		if bindings[binding] {
			return nil, fmt.Errorf("duplicate zone host/base path binding")
		}
		bindings[binding] = true
		if len(zone.App) > 4096 || !filepath.IsLocal(zone.App) || strings.ContainsAny(zone.App, "\\:\x00\r\n") {
			return nil, fmt.Errorf("zone %q: app must be a local relative path", zone.ID)
		}
		for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(zone.App)), "/") {
			if part != "." && ignored(part) {
				return nil, fmt.Errorf("zone %q: private/generated app directory is unsupported", zone.ID)
			}
		}
		zone.Root, err = resolveZoneApp(filepath.Join(manifest.Root, filepath.FromSlash(zone.App)))
		if err != nil {
			return nil, fmt.Errorf("zone %q: resolving app directory: %w", zone.ID, err)
		}
		if !pathWithin(zone.Root, manifest.Root) {
			return nil, fmt.Errorf("zone %q: app escapes manifest directory", zone.ID)
		}
	}
	return &manifest, nil
}

// Resolve the nearest existing ancestor, including a symlink before a missing
// descendant. EvalSymlinks on the complete missing path alone would miss that
// ancestor and could incorrectly accept an escaping development watch root.
func resolveZoneApp(name string) (string, error) {
	var missing []string
	ancestor := name
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	return resolved, nil
}

// ZonesWatcher preserves known application inputs through malformed manifests.
// Like SourceWatcher, one development supervisor owns it; calls are not concurrent.
type ZonesWatcher struct {
	roots    []string
	watchers map[string]*SourceWatcher
}

func (w *ZonesWatcher) Fingerprint(ctx context.Context, name string) (string, error) {
	data, path, readErr := readZonesManifest(name)
	manifest, manifestErr := parseZonesManifest(data, path)
	if readErr == nil && manifestErr == nil {
		w.roots = nil
		for _, zone := range manifest.Zones {
			addUnique(&w.roots, zone.Root)
		}
		sort.Strings(w.roots)
		retained := map[string]bool{}
		for _, root := range w.roots {
			retained[root] = true
		}
		for root := range w.watchers {
			if !retained[root] {
				delete(w.watchers, root)
			}
		}
	}
	if w.watchers == nil {
		w.watchers = map[string]*SourceWatcher{}
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "zones-v1:%d:%s:%d:", len(path), path, len(data))
	hash.Write(data)
	first := readErr
	if first == nil {
		first = manifestErr
	}
	for _, root := range w.roots {
		watcher := w.watchers[root]
		if watcher == nil {
			watcher = &SourceWatcher{}
			w.watchers[root] = watcher
		}
		value, err := watcher.Fingerprint(ctx, root)
		if err != nil && first == nil {
			first = err
		}
		fmt.Fprintf(hash, "%d:%s:%d:%s", len(root), root, len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil)), first
}

func ZonesFingerprint(ctx context.Context, name string) (string, error) {
	var watcher ZonesWatcher
	return watcher.Fingerprint(ctx, name)
}
