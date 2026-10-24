package environment

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type entry struct {
	value   string
	literal bool
}

const escapedDollar = '\x01'

func validName(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for i, c := range key {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func parse(data []byte, name string) (map[string]entry, error) {
	bad := func(line int) (map[string]entry, error) {
		return nil, fmt.Errorf("environment: %s:%d: invalid assignment or quoted value", name, line)
	}
	if !utf8.Valid(data) || strings.ContainsAny(string(data), "\x00\x01") {
		return bad(1)
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff")
	result := map[string]entry{}
	pos, line := 0, 1
	for pos < len(text) {
		for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t' || text[pos] == '\n') {
			if text[pos] == '\n' {
				line++
			}
			pos++
		}
		if pos == len(text) {
			break
		}
		if text[pos] == '#' {
			for pos < len(text) && text[pos] != '\n' {
				pos++
			}
			continue
		}
		startLine := line
		if strings.HasPrefix(text[pos:], "export ") || strings.HasPrefix(text[pos:], "export\t") {
			pos += 7
			for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
				pos++
			}
		}
		start := pos
		for pos < len(text) && text[pos] != '=' && text[pos] != '\n' {
			pos++
		}
		if pos == len(text) || text[pos] != '=' {
			return bad(startLine)
		}
		key := strings.TrimSpace(text[start:pos])
		if !validName(key) {
			return bad(startLine)
		}
		pos++
		for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
			pos++
		}
		var value strings.Builder
		literal := false
		quoted := false
		if pos < len(text) && (text[pos] == '\'' || text[pos] == '"') {
			quoted = true
			quote := text[pos]
			literal = quote == '\''
			pos++
			closed := false
			for pos < len(text) {
				c := text[pos]
				pos++
				if c == quote {
					closed = true
					break
				}
				if c == '\n' {
					line++
				}
				if c == '\\' && quote == '"' && pos < len(text) {
					next := text[pos]
					pos++
					if next == '\n' {
						line++
					}
					switch next {
					case 'n':
						c = '\n'
					case 'r':
						c = '\r'
					case 't':
						c = '\t'
					case '$':
						c = escapedDollar
					case '\\', '"':
						c = next
					default:
						value.WriteByte('\\')
						c = next
					}
				}
				value.WriteByte(c)
				if value.Len() > maxValue {
					return bad(startLine)
				}
			}
			if !closed {
				return bad(startLine)
			}
			for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
				pos++
			}
			if pos < len(text) && text[pos] != '\n' && text[pos] != '#' {
				return bad(startLine)
			}
			if pos < len(text) && text[pos] == '#' {
				for pos < len(text) && text[pos] != '\n' {
					pos++
				}
			}
		} else {
			for pos < len(text) && text[pos] != '\n' {
				c := text[pos]
				pos++
				if c == '#' {
					for pos < len(text) && text[pos] != '\n' {
						pos++
					}
					break
				}
				if c == '\\' && pos < len(text) && text[pos] == '$' {
					pos++
					c = escapedDollar
				}
				value.WriteByte(c)
				if value.Len() > maxValue {
					return bad(startLine)
				}
			}
		}
		v := value.String()
		if !quoted {
			v = strings.TrimSpace(v)
		}
		result[key] = entry{value: v, literal: literal}
		if len(result) > maxKeys {
			return nil, fmt.Errorf("environment: %s: exceeds 256 variables", name)
		}
	}
	return result, nil
}

func expand(e entry, lookup func(string) (string, error)) (string, error) {
	if e.literal {
		return e.value, nil
	}
	var out strings.Builder
	for i := 0; i < len(e.value); {
		c := e.value[i]
		i++
		if c == escapedDollar {
			out.WriteByte('$')
		} else if c == '$' {
			start := i
			name := ""
			if i < len(e.value) && e.value[i] == '{' {
				i++
				begin := i
				for i < len(e.value) && e.value[i] != '}' {
					i++
				}
				if i == len(e.value) {
					return "", fmt.Errorf("environment: unterminated variable reference")
				}
				name = e.value[begin:i]
				i++
				if !validName(name) {
					return "", fmt.Errorf("environment: unsupported variable reference")
				}
			} else {
				for i < len(e.value) && (e.value[i] == '_' || e.value[i] >= 'A' && e.value[i] <= 'Z' || e.value[i] >= 'a' && e.value[i] <= 'z' || i > start && e.value[i] >= '0' && e.value[i] <= '9') {
					i++
				}
				name = e.value[start:i]
			}
			if name == "" {
				out.WriteByte('$')
			} else {
				value, err := lookup(name)
				if err != nil {
					return "", err
				}
				out.WriteString(value)
			}
		} else {
			out.WriteByte(c)
		}
		if out.Len() > maxValue {
			return "", fmt.Errorf("environment: expanded value exceeds 64 KiB")
		}
	}
	return out.String(), nil
}
