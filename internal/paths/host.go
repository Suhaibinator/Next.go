//go:build !js

package paths

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// CanonicalHost validates an exact ASCII DNS/IP authority with an optional port.
// It never accepts credentials, paths, forwarded authorities or wildcards.
func CanonicalHost(raw string) (string, error) {
	if raw == "" || len(raw) > 253 || strings.ContainsAny(raw, "@/\\?# \t\r\n") {
		return "", fmt.Errorf("invalid locale host")
	}
	u, err := url.Parse("//" + raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("invalid locale host")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(host) == nil {
		for _, part := range strings.Split(host, ".") {
			if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
				return "", fmt.Errorf("invalid locale host")
			}
			for _, r := range part {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					return "", fmt.Errorf("locale hosts must be ASCII DNS names or IP addresses")
				}
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid locale port")
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.HasSuffix(raw, ":") {
		return "", fmt.Errorf("invalid locale port")
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]", nil
	}
	return host, nil
}
