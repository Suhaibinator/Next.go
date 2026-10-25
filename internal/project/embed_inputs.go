package project

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Read actual Go comment tokens, so directive-looking strings or block comments
// cannot cause unrelated files to enter the public build fingerprint.
func embeddedPatterns(name string, data []byte) []string {
	var scan scanner.Scanner
	set := token.NewFileSet()
	file := set.AddFile(name, set.Base(), len(data))
	scan.Init(file, data, nil, scanner.ScanComments)
	var result []string
	for {
		_, kind, literal := scan.Scan()
		if kind == token.EOF {
			break
		}
		if kind != token.COMMENT || !(strings.HasPrefix(literal, "//go:embed ") || strings.HasPrefix(literal, "//go:embed\t")) {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(literal, "//go:embed"))
		for text != "" {
			var value string
			if text[0] == '"' || text[0] == '`' {
				quoted, err := strconv.QuotedPrefix(text)
				if err != nil {
					break
				}
				value, err = strconv.Unquote(quoted)
				if err != nil {
					break
				}
				text = text[len(quoted):]
				if text != "" && !unicode.IsSpace(rune(text[0])) {
					break
				}
			} else {
				end := strings.IndexFunc(text, unicode.IsSpace)
				if end < 0 {
					end = len(text)
				}
				value = text[:end]
				text = text[end:]
			}
			if value != "" {
				result = append(result, value)
			}
			text = strings.TrimSpace(text)
		}
	}
	return result
}

func expandEmbedded(dir, pattern string, visit func(string) error) error {
	return expandEmbeddedSelected(dir, pattern, nil, visit)
}

func expandEmbeddedSelected(dir, pattern string, selectFile func(string) (string, bool), visit func(string) error) error {
	all := strings.HasPrefix(pattern, "all:")
	pattern = strings.TrimPrefix(pattern, "all:")
	if !fs.ValidPath(pattern) || strings.Contains(pattern, "\\") {
		return nil
	} // compiler diagnoses invalid patterns
	if _, err := path.Match(pattern, "probe"); err != nil {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
	if err != nil {
		return nil
	}
	for _, match := range matches {
		if err := filepath.WalkDir(match, func(name string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if d.Name() == "vendor" || d.Name() == ".git" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(name, "go.mod")); err == nil {
					return filepath.SkipDir
				}
				if name != match && !all && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if name != match && !all && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return nil
			}
			selected := name
			if selectFile != nil {
				var include bool
				selected, include = selectFile(name)
				if !include {
					return nil
				}
			}
			if environmentInput(name) || environmentInput(selected) {
				return fmt.Errorf("environment files cannot be embedded build inputs; load them at runtime")
			}
			// Go rejects embedded symlinks. Hash the link itself so fixing it is noticed.
			return visit(selected)
		}); err != nil {
			return err
		}
	}
	return nil
}

func environmentInput(name string) bool {
	value := strings.ToLower(filepath.Base(name))
	return value == ".env" || strings.HasPrefix(value, ".env.")
}

// Overlay source directives are relative to virtual package directories. Map
// physical matches back through that view, and include virtual-only file
// mappings (which OS globbing cannot discover). All backing files are already
// bounded/watched; this also preserves the explicit environment/embed guard.
func expandEmbeddedOverlay(dir, pattern string, replacements map[string]string, visit func(string) error) error {
	if len(replacements) == 0 {
		return expandEmbedded(dir, pattern, visit)
	}
	physical, _ := overlayBacking(replacements, dir)
	if physical == "" {
		return nil
	}
	selectFile := func(name string) (string, bool) {
		relative, err := filepath.Rel(physical, name)
		if err != nil {
			return "", false
		}
		virtual := filepath.Join(dir, relative)
		selected, _ := overlayBacking(replacements, virtual)
		return selected, selected != ""
	}
	if err := expandEmbeddedSelected(physical, pattern, selectFile, visit); err != nil {
		return err
	}
	names := make([]string, 0, len(replacements))
	for name := range replacements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := replacements[name]
		if target == "" || strings.HasSuffix(target, string(filepath.Separator)) || !pathWithin(name, dir) {
			continue
		}
		relative, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		if overlayEmbedMatches(filepath.ToSlash(relative), pattern) {
			if environmentInput(name) || environmentInput(target) {
				return fmt.Errorf("environment files cannot be embedded build inputs; load them at runtime")
			}
			if err := visit(target); err != nil {
				return err
			}
		}
	}
	return nil
}
func overlayEmbedMatches(relative, pattern string) bool {
	all := strings.HasPrefix(pattern, "all:")
	pattern = strings.TrimPrefix(pattern, "all:")
	if !fs.ValidPath(pattern) || strings.Contains(pattern, "\\") {
		return false
	}
	for candidate := relative; candidate != "."; candidate = path.Dir(candidate) {
		matched, err := path.Match(pattern, candidate)
		if err != nil {
			return false
		}
		if !matched {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(relative, candidate), "/")
		include := true
		for _, part := range strings.Split(suffix, "/") {
			if part == "vendor" || part == ".git" || !all && (strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_")) {
				include = false
				break
			}
		}
		if include {
			return true
		}
	}
	return false
}
