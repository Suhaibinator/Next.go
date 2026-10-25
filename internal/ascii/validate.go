// Package ascii validates the framework's fixed ASCII wire and HTML names.
package ascii

// HTMLName matches [A-Za-z][A-Za-z0-9:_-]* without normalizing the input.
// Callers own normalization and any additional length or semantic restrictions.
func HTMLName(value string) bool { return identifier(value, true) }

// Identifier matches [A-Za-z][A-Za-z0-9_-]*. It imposes no length bound.
func Identifier(value string) bool { return identifier(value, false) }

func letter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func identifier(value string, colon bool) bool {
	if len(value) == 0 || !letter(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !letter(c) && !(c >= '0' && c <= '9') && c != '_' && c != '-' && !(colon && c == ':') {
			return false
		}
	}
	return true
}

// LowerHex requires exactly length bytes, all lowercase hexadecimal ASCII.
func LowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
