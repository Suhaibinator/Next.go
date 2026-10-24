// Package actionwire defines the shared typed action protocol, without server
// or browser runtime dependencies.
package actionwire

import "strings"

const Protocol = "1"
const ProtocolHeader = "X-Nextgo-Action-Protocol"
const RevisionHeader = "X-Nextgo-Action-Revision"

func ValidName(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if !ValidSegment(segment) {
			return false
		}
	}
	return true
}
func ValidSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}
