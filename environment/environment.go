// Package environment loads bounded server-only application configuration.
// Files are never evaluated as shell code or automatically exported to WASM.
package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/Suhaibinator/Next.go/serveronly"
)

type Mode string

const (
	Development Mode = "development"
	Production  Mode = "production"
	Test        Mode = "test"
)
const maxFile = 256 << 10
const maxValue = 64 << 10
const maxKeys = 256

func files(mode Mode) ([]string, error) {
	if mode != Development && mode != Production && mode != Test {
		return nil, fmt.Errorf("environment: mode must be development, production or test")
	}
	names := []string{".env." + string(mode) + ".local"}
	if mode != Test {
		names = append(names, ".env.local")
	}
	return append(names, ".env."+string(mode), ".env"), nil
}

func read(dir, name string) ([]byte, error) {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFile {
		return nil, fmt.Errorf("environment: %s: unreadable, non-regular or oversized file", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("environment: %s: unable to open file", name)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || len(data) > maxFile {
		return nil, fmt.Errorf("environment: %s: unable to read bounded file", name)
	}
	return data, nil
}

// Load returns an owned process-style snapshot. Explicit base entries, including
// empty values, outrank files; earlier files outrank later files. Test mode skips
// .env.local. Missing files are ignored. No process environment is mutated.
func Load(dir string, mode Mode, base []string) ([]string, error) {
	names, err := files(mode)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("environment: directory is not readable")
	}
	values := map[string]string{}
	for _, pair := range base {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			values[key] = value
		}
	}
	raw := map[string]entry{}
	for _, name := range names {
		data, err := read(dir, name)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}
		parsed, err := parse(data, name)
		if err != nil {
			return nil, err
		}
		for key, value := range parsed {
			if _, set := values[key]; set {
				continue
			}
			if _, set := raw[key]; !set {
				raw[key] = value
			}
		}
		if len(raw) > maxKeys {
			return nil, fmt.Errorf("environment: files exceed 256 variables")
		}
	}
	visiting := map[string]bool{}
	heights := map[string]int{}
	total := 0
	var resolve func(string, int) (string, error)
	resolve = func(key string, depth int) (string, error) {
		if depth > 16 {
			return "", fmt.Errorf("environment: excessively nested variable reference")
		}
		if value, set := values[key]; set {
			if depth+heights[key] > 16 {
				return "", fmt.Errorf("environment: excessively nested variable reference")
			}
			return value, nil
		}
		e, set := raw[key]
		if !set {
			return "", nil
		}
		if depth > 16 || visiting[key] {
			return "", fmt.Errorf("environment: cyclic or excessively nested variable reference")
		}
		visiting[key] = true
		defer delete(visiting, key)
		height := 0
		value, err := expand(e, func(name string) (string, error) {
			value, err := resolve(name, depth+1)
			if n := heights[name] + 1; n > height {
				height = n
			}
			return value, err
		})
		heights[key] = height
		if err != nil {
			return "", err
		}
		total += len(value)
		if total > 1<<20 {
			return "", fmt.Errorf("environment: expanded file values exceed 1 MiB")
		}
		values[key] = value
		return value, nil
	}
	for key := range raw {
		if _, err := resolve(key, 0); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}

// Apply loads application files into the current process once, during native
// startup before Configure. It cannot affect Go package initialization or the
// compiler/runtime settings that were already read before main.
func Apply(dir string, mode Mode) error {
	values, err := Load(dir, mode, os.Environ())
	if err != nil {
		return err
	}
	for _, pair := range values {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("environment: unable to apply a variable")
			}
		}
	}
	return nil
}

// Fingerprint is a private development watch token. Never publish it as a
// bundle/build ID: low-entropy server values must not become public verifiers.
func Fingerprint(dir string, mode Mode) (string, error) {
	names, err := files(mode)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, name := range names {
		data, err := read(dir, name)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		if data != nil {
			hash.Write([]byte{1})
			hash.Write(data)
		}
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
