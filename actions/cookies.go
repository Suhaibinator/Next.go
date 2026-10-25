package actions

import (
	"fmt"
	"net/http"
	"strings"
)

const maxResponseCookies = 32
const maxResponseCookieBytes = 8 << 10
const maxResponseCookieTotalBytes = 32 << 10

// prepareCookieHeaders freezes all inputs and validates the entire set before
// returning any header strings. Never include cookie fields/values in errors:
// callers log these diagnostics and cookies may contain credentials.
func prepareCookieHeaders(cookies []*http.Cookie) ([]string, error) {
	if len(cookies) > maxResponseCookies {
		return nil, fmt.Errorf("actions: response cookie count exceeds limit")
	}
	frozen := make([]http.Cookie, len(cookies))
	for index, cookie := range cookies {
		invalid := func() error { return fmt.Errorf("actions: invalid response cookie at index %d", index) }
		if cookie == nil {
			return nil, invalid()
		}
		copyCookie := *cookie
		// Bound ignored metadata before copying it too, so a parsed cookie's
		// Unparsed backing slice does not create an unbounded snapshot.
		metadataBytes := len(copyCookie.Raw) + len(copyCookie.RawExpires)
		if len(copyCookie.Name) > maxResponseCookieBytes || len(copyCookie.Value) > maxResponseCookieBytes || len(copyCookie.Path) > maxResponseCookieBytes || len(copyCookie.Domain) > maxResponseCookieBytes || metadataBytes > maxResponseCookieBytes || len(copyCookie.Unparsed) > maxResponseCookieBytes {
			return nil, invalid()
		}
		copyCookie.Unparsed = append([]string(nil), cookie.Unparsed...)
		for i, value := range copyCookie.Unparsed {
			if len(value)+1 > maxResponseCookieBytes-metadataBytes {
				return nil, invalid()
			}
			metadataBytes += len(value) + 1
			if cookieControl(value) {
				return nil, invalid()
			}
			copyCookie.Unparsed[i] = strings.Clone(value)
		}
		if cookieControl(copyCookie.Raw) || cookieControl(copyCookie.RawExpires) || copyCookie.Valid() != nil {
			return nil, invalid()
		}
		if copyCookie.Path != "" && (!strings.HasPrefix(copyCookie.Path, "/") || strings.Contains(copyCookie.Path, "\\")) {
			return nil, invalid()
		}
		if strings.HasPrefix(copyCookie.Name, "__Secure-") && !copyCookie.Secure {
			return nil, invalid()
		}
		if strings.HasPrefix(copyCookie.Name, "__Host-") && (!copyCookie.Secure || copyCookie.Path != "/" || copyCookie.Domain != "") {
			return nil, invalid()
		}
		if copyCookie.SameSite < 0 || copyCookie.SameSite > http.SameSiteNoneMode || copyCookie.SameSite == http.SameSiteNoneMode && !copyCookie.Secure {
			return nil, invalid()
		}
		if !copyCookie.Expires.IsZero() && copyCookie.Expires.Year() > 9999 {
			return nil, invalid()
		}
		copyCookie.Name = strings.Clone(copyCookie.Name)
		copyCookie.Value = strings.Clone(copyCookie.Value)
		copyCookie.Path = strings.Clone(copyCookie.Path)
		copyCookie.Domain = strings.Clone(copyCookie.Domain)
		copyCookie.Raw = strings.Clone(copyCookie.Raw)
		copyCookie.RawExpires = strings.Clone(copyCookie.RawExpires)
		frozen[index] = copyCookie
	}
	type identity struct{ name, domain, path string }
	seen := map[identity]bool{}
	var headers []string
	total := 0
	for index := range frozen {
		cookie := &frozen[index]
		key := identity{cookie.Name, strings.TrimPrefix(strings.ToLower(cookie.Domain), "."), cookie.Path}
		if seen[key] {
			return nil, fmt.Errorf("actions: duplicate response cookie identity at index %d", index)
		}
		seen[key] = true
		value := cookie.String()
		if value == "" || len(value) > maxResponseCookieBytes || len(value) > maxResponseCookieTotalBytes-total {
			return nil, fmt.Errorf("actions: response cookie header bytes exceed limit")
		}
		total += len(value)
		headers = append(headers, value)
	}
	return headers, nil
}

func cookieControl(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 32 || value[i] == 127 {
			return true
		}
	}
	return false
}
