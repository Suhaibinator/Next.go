package project

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
			if value := strings.ToLower(d.Name()); value == ".env" || strings.HasPrefix(value, ".env.") {
				return fmt.Errorf("environment files cannot be embedded build inputs; load them at runtime")
			}
			// Go rejects embedded symlinks. Hash the link itself so fixing it is noticed.
			return visit(name)
		}); err != nil {
			return err
		}
	}
	return nil
}
