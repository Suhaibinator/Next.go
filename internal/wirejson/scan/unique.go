package scan

import (
	"errors"
	"unicode/utf8"
)

var (
	errSyntax    = errors.New("invalid JSON")
	errDuplicate = errors.New("duplicate or invalid object key")
	errDepth     = errors.New("JSON nesting exceeds 64")
)

// Unique validates one JSON value, rejecting duplicate decoded object keys and
// values nested more than 64 levels below the root. Like encoding/json's legacy
// decoder, it accepts malformed UTF-8 and unpaired UTF-16 surrogates in strings,
// replacing them with U+FFFD when comparing keys. It does not convert numbers.
// Callers bound the input size; scanner storage is bounded by that input and
// the nesting limit. No decoded string values or number values are retained.
func Unique(data []byte) error {
	s := uniqueScanner{data: data}
	if err := s.value(0); err != nil {
		return err
	}
	s.space()
	if s.pos != len(data) {
		return errSyntax
	}
	return nil
}

type uniqueScanner struct {
	data []byte
	pos  int
}

func (s *uniqueScanner) space() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}

func (s *uniqueScanner) value(depth int) error {
	if depth > 64 {
		return errDepth
	}
	s.space()
	if s.pos == len(s.data) {
		return errSyntax
	}
	switch s.data[s.pos] {
	case '{':
		return s.object(depth)
	case '[':
		return s.array(depth)
	case '"':
		_, err := s.string(false)
		return err
	case 't':
		return s.literal("true")
	case 'f':
		return s.literal("false")
	case 'n':
		return s.literal("null")
	default:
		return s.number()
	}
}

func (s *uniqueScanner) literal(value string) error {
	if len(s.data)-s.pos < len(value) || string(s.data[s.pos:s.pos+len(value)]) != value {
		return errSyntax
	}
	s.pos += len(value)
	return nil
}

func (s *uniqueScanner) take(c byte) bool {
	if s.pos < len(s.data) && s.data[s.pos] == c {
		s.pos++
		return true
	}
	return false
}

func (s *uniqueScanner) object(depth int) error {
	s.pos++
	s.space()
	if s.take('}') {
		return nil
	}
	seen := make(map[string]struct{})
	for {
		key, err := s.string(true)
		if err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			return errDuplicate
		}
		seen[key] = struct{}{}
		s.space()
		if !s.take(':') {
			return errSyntax
		}
		if err := s.value(depth + 1); err != nil {
			return err
		}
		s.space()
		if s.take('}') {
			return nil
		}
		if !s.take(',') {
			return errSyntax
		}
		s.space()
	}
}

func (s *uniqueScanner) array(depth int) error {
	s.pos++
	s.space()
	if s.take(']') {
		return nil
	}
	for {
		if err := s.value(depth + 1); err != nil {
			return err
		}
		s.space()
		if s.take(']') {
			return nil
		}
		if !s.take(',') {
			return errSyntax
		}
	}
}

func digit(c byte) bool { return c >= '0' && c <= '9' }

func (s *uniqueScanner) number() error {
	s.take('-')
	if s.pos == len(s.data) {
		return errSyntax
	}
	if !s.take('0') {
		if s.data[s.pos] < '1' || s.data[s.pos] > '9' {
			return errSyntax
		}
		for s.pos < len(s.data) && digit(s.data[s.pos]) {
			s.pos++
		}
	}
	if s.take('.') {
		start := s.pos
		for s.pos < len(s.data) && digit(s.data[s.pos]) {
			s.pos++
		}
		if s.pos == start {
			return errSyntax
		}
	}
	if s.take('e') || s.take('E') {
		if !s.take('+') {
			s.take('-')
		}
		start := s.pos
		for s.pos < len(s.data) && digit(s.data[s.pos]) {
			s.pos++
		}
		if s.pos == start {
			return errSyntax
		}
	}
	return nil
}

func hex(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	default:
		return 0, false
	}
}

func hex4(data []byte) (rune, bool) {
	if len(data) < 4 {
		return 0, false
	}
	var value rune
	for _, c := range data[:4] {
		n, ok := hex(c)
		if !ok {
			return 0, false
		}
		value = value<<4 | n
	}
	return value, true
}

// string validates the token, decoding only object keys. Ordinary value strings
// need no allocation, including escaped strings and malformed UTF-8.
func (s *uniqueScanner) string(key bool) (string, error) {
	if !s.take('"') {
		return "", errSyntax
	}
	start := s.pos
	segment := start
	var decoded []byte
	for s.pos < len(s.data) {
		c := s.data[s.pos]
		if c == '"' {
			end := s.pos
			s.pos++
			if !key {
				return "", nil
			}
			if decoded == nil {
				return string(s.data[start:end]), nil
			}
			decoded = append(decoded, s.data[segment:end]...)
			return string(decoded), nil
		}
		if c < ' ' {
			return "", errSyntax
		}
		if c == '\\' {
			escape := s.pos
			s.pos++
			if s.pos == len(s.data) {
				return "", errSyntax
			}
			var value rune
			switch s.data[s.pos] {
			case '"', '\\', '/':
				value = rune(s.data[s.pos])
				s.pos++
			case 'b', 'f', 'n', 'r', 't':
				switch s.data[s.pos] {
				case 'b':
					value = '\b'
				case 'f':
					value = '\f'
				case 'n':
					value = '\n'
				case 'r':
					value = '\r'
				case 't':
					value = '\t'
				}
				s.pos++
			case 'u':
				s.pos++
				var ok bool
				value, ok = hex4(s.data[s.pos:])
				if !ok {
					return "", errSyntax
				}
				s.pos += 4
				if value >= 0xd800 && value <= 0xdfff {
					if value <= 0xdbff && len(s.data)-s.pos >= 6 && s.data[s.pos] == '\\' && s.data[s.pos+1] == 'u' {
						low, valid := hex4(s.data[s.pos+2:])
						if valid && low >= 0xdc00 && low <= 0xdfff {
							value = 0x10000 + (value-0xd800)<<10 + low - 0xdc00
							s.pos += 6
						} else {
							value = utf8.RuneError
						}
					} else {
						value = utf8.RuneError
					}
				}
			default:
				return "", errSyntax
			}
			if key {
				decoded = append(decoded, s.data[segment:escape]...)
				decoded = utf8.AppendRune(decoded, value)
				segment = s.pos
			}
			continue
		}
		if key && c >= utf8.RuneSelf {
			value, size := utf8.DecodeRune(s.data[s.pos:])
			if value == utf8.RuneError && size == 1 {
				decoded = append(decoded, s.data[segment:s.pos]...)
				decoded = utf8.AppendRune(decoded, utf8.RuneError)
				s.pos++
				segment = s.pos
			} else {
				s.pos += size
			}
			continue
		}
		s.pos++
	}
	return "", errSyntax
}
