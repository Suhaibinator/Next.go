package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxCallerOverlayBytes = 2 << 20
const maxCallerOverlayMappings = 4096

type buildOverlay struct {
	Replace map[string]string
	Files   []string
	Sources map[string]string
}

// Match cmd/go/internal/fsys.abs: overlay paths are relative to the Go command's
// working directory, not to the directory containing the JSON manifest.
func overlayAbsolute(root, name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	if volume := filepath.VolumeName(root); volume != "" && os.IsPathSeparator(name[0]) {
		return filepath.Join(volume, name)
	}
	return filepath.Join(root, name)
}
func pathWithin(name, root string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// Collision checks also cover a symlinked spelling of a not-yet-created
// generated path. Compiler mappings themselves retain cmd/go's exact spelling.
func overlayCanonicalPath(name string) string {
	var suffix []string
	current := filepath.Clean(name)
	for {
		if _, err := os.Lstat(current); err == nil {
			if resolved, err := filepath.EvalSymlinks(current); err == nil {
				for i := len(suffix) - 1; i >= 0; i-- {
					resolved = filepath.Join(resolved, suffix[i])
				}
				return resolved
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(name)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
func frameworkOverlayPath(root, name string) bool {
	name = overlayCanonicalPath(name)
	generated := filepath.Join(root, ".nextgo", "generated")
	if pathWithin(name, generated) {
		return true
	}
	relative, err := filepath.Rel(filepath.Join(root, ".nextgo"), name)
	return err == nil && strings.HasPrefix(strings.Split(relative, string(filepath.Separator))[0], "build-")
}
func readCallerOverlay(ctx context.Context, root string) (result buildOverlay, first error) {
	root = canonicalInput(root)
	result.Replace = map[string]string{}
	result.Sources = map[string]string{}
	data, err := goInputJSON(ctx, root, "env", "-json", "GOFLAGS")
	if err != nil {
		return result, err
	}
	var environment struct{ GOFLAGS string }
	if err := json.Unmarshal(data, &environment); err != nil {
		return result, fmt.Errorf("read overlay Go flags: %w", err)
	}
	words, err := goFlagWords(environment.GOFLAGS)
	if err != nil {
		return result, fmt.Errorf("invalid GOFLAGS quoting")
	}
	manifest := ""
	for _, word := range words {
		flag := strings.TrimPrefix(strings.TrimPrefix(word, "-"), "-")
		if flag == "overlay" {
			return result, fmt.Errorf("GOFLAGS -overlay requires its value in the same flag")
		}
		if value, ok := strings.CutPrefix(flag, "overlay="); ok {
			manifest = value
		}
	}
	if manifest == "" {
		return result, nil
	}
	manifest = overlayAbsolute(root, manifest)
	result.Files = append(result.Files, manifest)
	info, err := os.Stat(manifest)
	if err != nil {
		return result, fmt.Errorf("read caller overlay: %w", err)
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("caller overlay manifest must be a regular file")
	}
	if info.Size() > maxCallerOverlayBytes {
		return result, fmt.Errorf("caller overlay exceeds 2 MiB")
	}
	file, err := os.Open(manifest)
	if err != nil {
		return result, fmt.Errorf("read caller overlay: %w", err)
	}
	encoded, err := io.ReadAll(inputReader{ctx, io.LimitReader(file, maxCallerOverlayBytes+1)})
	_ = file.Close()
	if err != nil {
		return result, err
	}
	if len(encoded) > maxCallerOverlayBytes {
		return result, fmt.Errorf("caller overlay exceeds 2 MiB")
	}
	var config struct{ Replace map[string]string }
	if err := json.Unmarshal(encoded, &config); err != nil {
		return result, fmt.Errorf("parse caller overlay JSON: %w", err)
	}
	if len(config.Replace) > maxCallerOverlayMappings {
		return result, fmt.Errorf("caller overlay exceeds %d mappings", maxCallerOverlayMappings)
	}
	record := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	keys := make([]string, 0, len(config.Replace))
	for key := range config.Replace {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var total int64
	filesSeen := map[string]bool{manifest: true}
	appendFile := func(name string) {
		if !filesSeen[name] {
			filesSeen[name] = true
			result.Files = append(result.Files, name)
		}
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if key == "" || len(key) > 8192 || len(config.Replace[key]) > 8192 {
			record(fmt.Errorf("caller overlay has an empty or oversized path"))
			continue
		}
		from, to := overlayAbsolute(root, key), overlayAbsolute(root, config.Replace[key])
		if _, exists := result.Replace[from]; exists {
			record(fmt.Errorf("caller overlay has duplicate normalized paths"))
			continue
		}
		if frameworkOverlayPath(root, from) || pathWithin(filepath.Join(root, ".nextgo", "generated"), overlayCanonicalPath(from)) || to != "" && frameworkOverlayPath(root, to) {
			record(fmt.Errorf("caller overlay conflicts with framework generated paths"))
			continue
		}
		result.Replace[from] = to
		if to == "" {
			continue
		}
		info, err := os.Stat(to)
		if err != nil {
			appendFile(to)
			if filepath.Ext(from) == ".go" {
				result.Sources[from] = to
			}
			record(fmt.Errorf("read caller overlay backing file: %w", err))
			continue
		}
		if info.IsDir() {
			record(fmt.Errorf("caller overlay directory backing is unsupported by Go; map individual files"))
			continue
		}

		appendFile(to)
		if !info.Mode().IsRegular() || info.Size() > maxInputFile || total+info.Size() > maxInputTotal {
			record(fmt.Errorf("caller overlay backing inputs exceed type/byte limits"))
			continue
		}
		total += info.Size()
		if filepath.Ext(from) == ".go" {
			result.Sources[from] = to
		}
	}
	for virtual := range result.Sources {
		if backing, ok := overlayBacking(result.Replace, virtual); ok {
			if backing == "" {
				delete(result.Sources, virtual)
			} else {
				result.Sources[virtual] = backing
			}
		}
	}
	sort.Strings(result.Files)
	return result, first
}

// overlayBacking resolves file replacements and deletions. Go overlays cannot
// replace a whole directory; a deleted path also hides its descendants.
func overlayBacking(replacements map[string]string, name string) (string, bool) {
	if target, ok := replacements[name]; ok {
		return target, true
	}
	for from, to := range replacements {
		if to == "" && pathWithin(name, from) {
			return "", true
		}
	}
	return name, false
}

func validateGeneratedVirtualRoot(root string) error {
	if _, err := os.Lstat(filepath.Join(root, ".nextgo", "generated")); err == nil {
		return fmt.Errorf("framework generated virtual path already exists on disk; remove .nextgo/generated and rebuild")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Generated virtual packages have stable module/VCS/source identity; their
// backing files and overlay manifests stay private to each unique generation.
func generatedEntryOverlay(ctx context.Context, root, physical, kind string) (string, string, error) {
	caller, err := readCallerOverlay(ctx, root)
	if err != nil {
		return "", "", err
	}
	virtualRoot := filepath.Join(root, ".nextgo", "generated")
	if err := validateGeneratedVirtualRoot(root); err != nil {
		return "", "", err
	}
	virtual := filepath.Join(virtualRoot, kind)
	replacements := caller.Replace
	err = filepath.WalkDir(physical, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated entrypoint contains a symlink")
		}
		if len(replacements) >= maxInputFiles {
			return fmt.Errorf("generated entrypoint exceeds file limit")
		}
		relative, err := filepath.Rel(physical, name)
		if err != nil {
			return err
		}
		replacements[filepath.Join(virtual, relative)] = name
		return nil
	})
	if err != nil {
		return "", "", err
	}
	encoded, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		return "", "", err
	}
	overlay := filepath.Join(filepath.Dir(physical), "overlay-"+kind+".json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		return "", "", err
	}
	return virtual, overlay, nil
}
