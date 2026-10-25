package project

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The first offline slice accepts pure styles, not CSS which discovers more
// resources. This deliberately conservative lexer is not a CSS implementation:
// escaped grammar, imports, resource functions and unknown functions require a
// later explicit dependency contract rather than a mutable URL cache alias.
func validateOfflineCSS(data []byte) error {
	if !utf8.Valid(data) || bytes.ContainsRune(data, '\\') {
		return fmt.Errorf("escaped CSS grammar is unsupported for managed offline styles")
	}
	plain := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if i+1 < len(data) && data[i] == '/' && data[i+1] == '*' {
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				return fmt.Errorf("unterminated CSS comment")
			}
			i += end + 4
			continue
		}
		if data[i] == '\'' || data[i] == '"' {
			quote := data[i]
			i++
			for i < len(data) && data[i] != quote {
				i++
			}
			if i == len(data) {
				return fmt.Errorf("unterminated CSS string")
			}
			i++
			continue
		}
		plain = append(plain, data[i])
		i++
	}
	text := strings.ToLower(string(plain))
	for i := 0; i < len(text); {
		if !offlineCSSIdent(text[i]) {
			i++
			continue
		}
		start := i
		for i < len(text) && offlineCSSIdent(text[i]) {
			i++
		}
		word := text[start:i]
		if start > 0 && text[start-1] == '@' && word == "import" {
			return fmt.Errorf("CSS imports are unsupported for managed offline styles")
		}
		next := i
		for next < len(text) && strings.ContainsRune(" \t\r\n\f", rune(text[next])) {
			next++
		}
		if next < len(text) && text[next] == '(' && !offlinePureCSSFunction(word) {
			return fmt.Errorf("CSS function %q requires an unsupported offline resource or grammar", word)
		}
	}
	return nil
}
func offlineCSSIdent(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}
func offlinePureCSSFunction(name string) bool {
	switch name {
	case "rgb", "rgba", "hsl", "hsla", "hwb", "lab", "lch", "oklab", "oklch", "color", "color-mix", "light-dark",
		"calc", "min", "max", "clamp", "var", "env", "round", "mod", "rem", "abs", "sign", "pow", "sqrt", "hypot", "log", "exp",
		"sin", "cos", "tan", "asin", "acos", "atan", "atan2", "repeat", "minmax", "fit-content",
		"linear-gradient", "repeating-linear-gradient", "radial-gradient", "repeating-radial-gradient", "conic-gradient", "repeating-conic-gradient",
		"cubic-bezier", "steps", "matrix", "matrix3d", "translate", "translatex", "translatey", "translatez", "translate3d",
		"scale", "scalex", "scaley", "scalez", "scale3d", "rotate", "rotatex", "rotatey", "rotatez", "rotate3d", "skew", "skewx", "skewy", "perspective",
		"blur", "brightness", "contrast", "drop-shadow", "grayscale", "hue-rotate", "invert", "opacity", "saturate", "sepia",
		"counter", "counters", "local", "not", "is", "where", "has", "nth-child", "nth-last-child", "nth-of-type", "nth-last-of-type", "lang", "dir", "selector", "supports", "media", "and", "or":
		return true
	}
	return false
}
