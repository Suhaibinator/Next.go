package navigation

const ShellProtocolHeader = "X-Nextgo-Shell-Protocol"
const ShellHeader = "X-Nextgo-Shell"
const ShellStaleHeader = "X-Nextgo-Shell-Stale"
const ShellVersion = 1
const MaxShellStaleMillis = 30000

// ValidShellID checks an opaque generation digest. It is not authorization.
func ValidShellID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
