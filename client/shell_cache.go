package client

import (
	"strconv"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// This opt-in is only for a negotiated public-shell envelope, never a complete
// personalized response. Generic page-cache privacy rules remain unchanged.
func shellCacheLifetime(header interface {
	Get(string) string
	Values(string) []string
}, shell string) time.Duration {
	if !navigation.ValidShellID(shell) || len(header.Values(navigation.ShellProtocolHeader)) != 1 || header.Get(navigation.ShellProtocolHeader) != strconv.Itoa(navigation.ShellVersion) || len(header.Values("X-Nextgo-Prefetch")) != 1 || header.Get("X-Nextgo-Prefetch") != "shell" || len(header.Values("Set-Cookie")) != 0 {
		return 0
	}
	for _, vary := range header.Values("Vary") {
		for _, field := range strings.Split(vary, ",") {
			if strings.TrimSpace(field) == "*" {
				return 0
			}
		}
	}
	private, noStore := false, false
	for _, value := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(value, ",") {
			switch strings.TrimSpace(strings.ToLower(directive)) {
			case "private":
				private = true
			case "no-store":
				noStore = true
			case "public":
				return 0
			}
		}
	}
	if !private || !noStore || len(header.Values("Age")) != 0 {
		return 0
	}
	values := header.Values(navigation.ShellStaleHeader)
	if len(values) != 1 || values[0] == "" || len(values[0]) > 5 {
		return 0
	}
	for _, c := range values[0] {
		if c < '0' || c > '9' {
			return 0
		}
	}
	millis, err := strconv.Atoi(values[0])
	if err != nil || millis <= 0 || millis > navigation.MaxShellStaleMillis {
		return 0
	}
	return time.Duration(millis) * time.Millisecond
}
