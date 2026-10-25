//go:build !js

package buildassets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type quota struct {
	files int
	bytes int64
}

func (q *quota) add(size int64) error {
	q.files++
	q.bytes += size
	if size > maxFile || q.bytes > maxTotal || q.files > maxFiles {
		return fmt.Errorf("assets exceed 64 MiB/file, 256 MiB total, or 10000 files")
	}
	return nil
}

func noSymlinks(root *os.Root, name string) error {
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i := range parts {
		info, err := root.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("asset paths cannot contain symlinks: %s", name)
		}
	}
	return nil
}

func readRegular(ctx context.Context, root *os.Root, name string, q *quota) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := noSymlinks(root, name); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("asset must be a regular file: %s", name)
	}
	if err := q.add(info.Size()); err != nil {
		return nil, err
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("asset changed while opening: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, info.Size()+1))
	if err == nil && int64(len(data)) != info.Size() {
		err = fmt.Errorf("asset changed size while reading: %s", name)
	}
	return data, err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func snapshot(ctx context.Context, source, destination string, inputs []string) error {
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()
	seen, q := map[string]bool{}, &quota{}
	for _, input := range inputs {
		if err := noSymlinks(root, input); err != nil {
			return err
		}
		err := fs.WalkDir(root.FS(), input, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = safePath(name, false); err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("asset input cannot be a symlink: %s", name)
			}
			if seen[name] {
				return nil
			}
			seen[name] = true
			target := filepath.Join(destination, filepath.FromSlash(name))
			if d.IsDir() {
				return os.MkdirAll(target, 0700)
			}
			data, err := readRegular(ctx, root, name, q)
			if err != nil {
				return err
			}
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			return os.WriteFile(target, data, 0600)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func collect(ctx context.Context, source, destination string, outputs []string) error {
	return collectWithQuota(ctx, source, destination, outputs, &quota{})
}

// The v2 graph shares one ledger across canonical outputs, dependency copies
// and public copies. The v1 collector retains its original per-tree quota.
func collectWithQuota(ctx context.Context, source, destination string, outputs []string, q *quota) error {
	declared, found := map[string]bool{}, map[string]bool{}
	for _, output := range outputs {
		declared[output] = true
	}
	root, err := openDirectory(source)
	if err != nil {
		return err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if err = safePath(name, true); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated asset cannot be a symlink: %s", name)
		}
		if d.IsDir() {
			return nil
		}
		if !declared[name] {
			return fmt.Errorf("undeclared generated asset: %s", name)
		}
		data, err := readRegular(ctx, root, name, q)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err = os.WriteFile(target, data, 0644); err != nil {
			return err
		}
		found[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	for _, output := range outputs {
		if !found[output] {
			return fmt.Errorf("declared generated asset is missing: %s", output)
		}
	}
	return nil
}

func openDirectory(name string) (*os.Root, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("asset directory must not be a symlink: %s", name)
	}
	return os.OpenRoot(name)
}

// Merge adds generated assets to a new build's staged public tree. It refuses
// collisions (including case variants), symlinks, and private filenames. Use a
// disposable destination: the complete framework build publishes it on success.
func Merge(ctx context.Context, generated, public string) error {
	type entry struct {
		name      string
		directory bool
	}
	entries := map[string]entry{}
	for _, dir := range []string{public, generated} {
		root, err := openDirectory(dir)
		if err != nil {
			return err
		}
		err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if name == "." {
				return nil
			}
			if err = safePath(name, dir == generated); err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("public asset cannot be a symlink: %s", name)
			}
			key := strings.ToLower(name)
			if old, exists := entries[key]; exists && (old.name != name || !d.IsDir() || !old.directory) {
				return fmt.Errorf("public/generated asset collision: %s", name)
			}
			for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
				if old, ok := entries[strings.ToLower(parent)]; ok && !old.directory {
					return fmt.Errorf("public/generated asset collision: %s", name)
				}
			}
			entries[key] = entry{name, d.IsDir()}
			return nil
		})
		root.Close()
		if err != nil {
			return err
		}
	}
	root, err := openDirectory(generated)
	if err != nil {
		return err
	}
	defer root.Close()
	q := &quota{}
	return fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		target := filepath.Join(public, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := readRegular(ctx, root, name, q)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}

func record(h hash.Hash, name, kind string, data []byte) {
	fmt.Fprintf(h, "%d:%s:%s:%d:", len(name), name, kind, len(data))
	_, _ = h.Write(data)
}

func digestTree(ctx context.Context, dir string) (string, error) {
	h := sha256.New()
	root, err := openDirectory(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	q := &quota{}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := readRegular(ctx, root, name, q)
		if err == nil {
			record(h, name, "file", data)
		}
		return err
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

// Fingerprint watches manifest bytes and declared inputs without executing
// tools. Invalid configuration still hashes its raw bytes, so development can
// recover on the next edit; Load reports configuration errors during builds.
// Missing inputs and symlinks are recorded, never followed.
func Fingerprint(ctx context.Context, directory string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	h := sha256.New()
	data, present, err := manifest(directory)
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	record(h, ManifestName, "manifest", data)
	var p Pipeline
	if err := json.Unmarshal(data, &p); err != nil {
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	inputs, seen, q := []string{}, map[string]bool{}, &quota{}
	if len(p.Tools) > 64 {
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	for _, t := range p.Tools {
		if len(t.Inputs) > 256 {
			continue
		}
		for _, input := range t.Inputs {
			if safePath(input, false) == nil {
				inputs = append(inputs, input)
			}
		}
	}
	sort.Strings(inputs)
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := noSymlinks(root, input); err != nil {
			record(h, input, "unavailable", []byte(err.Error()))
			continue
		}
		err := fs.WalkDir(root.FS(), input, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				record(h, name, "unavailable", []byte(err.Error()))
				return nil
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if seen[name] {
				return nil
			}
			seen[name] = true
			if err = safePath(name, false); err != nil {
				record(h, name, "private", nil)
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				target, _ := os.Readlink(filepath.Join(directory, filepath.FromSlash(name)))
				record(h, name, "symlink", []byte(target))
				return nil
			}
			if d.IsDir() {
				record(h, name, "dir", nil)
				return nil
			}
			if !d.Type().IsRegular() {
				record(h, name, "special", []byte(d.Type().String()))
				return nil
			}
			data, err := readRegular(ctx, root, name, q)
			if err == nil {
				record(h, name, "file", data)
			}
			return err
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
