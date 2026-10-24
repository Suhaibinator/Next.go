// Package paths shares deployment URL rules between native and WASM targets.
package paths

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Config struct {
	TrailingSlash bool   `json:"trailingSlash,omitempty"`
	BasePath      string `json:"basePath"`
	AssetPrefix   string `json:"assetPrefix"`
}
type contextKey struct{}

func WithContext(ctx context.Context, c Config) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}
func FromContext(ctx context.Context) Config {
	if ctx == nil {
		return Config{}
	}
	c, _ := ctx.Value(contextKey{}).(Config)
	return c
}

func (c Config) Validate() error {
	if len(c.BasePath) > 8192 || len(c.AssetPrefix) > 8192 {
		return fmt.Errorf("deployment prefixes must be at most 8 KiB each")
	}
	if err := prefix(c.BasePath); err != nil {
		return fmt.Errorf("base path: %w", err)
	}
	if c.AssetPrefix == "" || c.AssetPrefix == "/" {
		return nil
	}
	if strings.HasPrefix(c.AssetPrefix, "/") {
		if err := prefix(c.AssetPrefix); err != nil {
			return fmt.Errorf("asset prefix: %w", err)
		}
		return nil
	}
	u, err := url.Parse(c.AssetPrefix)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(c.AssetPrefix, "#") || u.RawPath != "" {
		return fmt.Errorf("asset prefix requires a local prefix or HTTP(S) URL without credentials, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("asset prefix has an invalid port")
		}
	}
	if err := prefix(u.Path); err != nil {
		return fmt.Errorf("asset prefix: %w", err)
	}
	return nil
}
func prefix(value string) error {
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("use an absolute path prefix without a trailing slash, or empty for root")
	}
	for _, part := range strings.Split(value[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("prefix contains an empty or dot segment")
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
				return fmt.Errorf("prefix segments must use ASCII letters, digits, - . _ or ~")
			}
		}
	}
	return nil
}

// Strip matches complete literal prefix segments. Encoded spellings of a base
// prefix are not aliases. The returned path remains escaped for route matching.
func (c Config) Strip(escapedPath string) (string, bool) { return Strip(c.BasePath, escapedPath) }
func Strip(base, escapedPath string) (string, bool) {
	if base == "" {
		return escapedPath, strings.HasPrefix(escapedPath, "/")
	}
	if escapedPath == base {
		return "/", true
	}
	if strings.HasPrefix(escapedPath, base+"/") {
		return strings.TrimPrefix(escapedPath, base), true
	}
	return "", false
}

// URL resolves a decoded/escaped application-absolute URL without changing its
// query or fragment. Passing a browser-mounted URL would apply the base twice.
func (c Config) URL(local string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	u, err := localURL(local)
	if err != nil {
		return "", err
	}
	path := c.SlashPath(u.EscapedPath())
	if path == "/" && c.BasePath != "" && !c.TrailingSlash {
		path = ""
	}
	return c.BasePath + path + suffix(u), nil
}
func (c Config) AssetURL(local string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	u, err := localURL(local)
	if err != nil {
		return "", err
	}
	return c.EffectiveAssets() + "/assets" + u.EscapedPath() + suffix(u), nil
}
func (c Config) RuntimeURL(build string) string {
	return c.EffectiveAssets() + "/_nextgo/" + url.PathEscape(build) + "/"
}
func (c Config) EffectiveAssets() string {
	if c.AssetPrefix == "/" {
		return ""
	}
	if c.AssetPrefix != "" {
		return c.AssetPrefix
	}
	return c.BasePath
}
func (c Config) AssetPath() string {
	if strings.HasPrefix(c.EffectiveAssets(), "/") || c.EffectiveAssets() == "" {
		return c.EffectiveAssets()
	}
	u, _ := url.Parse(c.EffectiveAssets())
	return u.Path
}
func suffix(u *url.URL) string {
	out := ""
	if u.RawQuery != "" || u.ForceQuery {
		out = "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}
func localURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || u.Host != "" || u.Scheme != "" || u.User != nil || strings.Contains(value, "\\") {
		return nil, fmt.Errorf("URL must be an application-absolute path without an authority")
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return nil, fmt.Errorf("URL contains control characters")
		}
	}
	path := u.EscapedPath()
	if path != "/" {
		for _, encoded := range strings.Split(strings.TrimPrefix(strings.TrimSuffix(path, "/"), "/"), "/") {
			part, err := url.PathUnescape(encoded)
			if err != nil || !utf8.ValidString(part) || part == "" || part == "." || part == ".." || strings.ContainsAny(part, "/\\") {
				return nil, fmt.Errorf("URL contains an invalid path segment")
			}
			for _, r := range part {
				if r < 32 || r == 127 {
					return nil, fmt.Errorf("URL contains an invalid path character")
				}
			}
		}
	}
	return u, nil
}

// IsAssetURL permits only configured asset locations, never another authority or
// a path that escapes its asset subtree after percent decoding.
func (c Config) IsAssetURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return false
	}
	prefix, err := url.Parse(c.EffectiveAssets())
	if err != nil {
		return false
	}
	if u.Scheme != prefix.Scheme || u.Host != prefix.Host {
		return false
	}
	local, ok := Strip(prefix.EscapedPath(), u.EscapedPath())
	if !ok || !strings.HasPrefix(local, "/assets/") {
		return false
	}
	_, err = localURL(local)
	return err == nil
}
