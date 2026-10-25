//go:build !js

package metadatafiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

// Manifest deliberately supports a bounded static web-manifest subset. URLs
// are local to the global application mount; remote schemes and SVG are not
// supported. Values are public browser metadata, never session credentials.
type Manifest struct {
	Name            string         `json:"name,omitempty"`
	ShortName       string         `json:"short_name,omitempty"`
	Description     string         `json:"description,omitempty"`
	Lang            string         `json:"lang,omitempty"`
	Dir             string         `json:"dir,omitempty"`
	ID              string         `json:"id,omitempty"`
	StartURL        string         `json:"start_url,omitempty"`
	Scope           string         `json:"scope,omitempty"`
	Display         string         `json:"display,omitempty"`
	Orientation     string         `json:"orientation,omitempty"`
	BackgroundColor string         `json:"background_color,omitempty"`
	ThemeColor      string         `json:"theme_color,omitempty"`
	Icons           []ManifestIcon `json:"icons,omitempty"`
}

type ManifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes,omitempty"`
	Type    string `json:"type,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

func MarshalManifest(value Manifest, options Options) ([]byte, error) {
	if err := validateManifest(value, options); err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data)+1 > MaxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds %d bytes", MaxManifestBytes)
	}
	return append(data, '\n'), nil
}

func ParseManifest(data []byte, options Options) (Manifest, error) {
	var result Manifest
	if len(data) == 0 || len(data) > MaxManifestBytes || !utf8.Valid(data) || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return result, fmt.Errorf("invalid manifest size or encoding")
	}
	if err := wirejson.Unique(data); err != nil {
		return result, fmt.Errorf("invalid or duplicate manifest JSON")
	}
	if err := wirejson.Strict(data, &result); err != nil {
		return result, fmt.Errorf("manifest has unsupported fields or types")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil || keys == nil {
		return result, fmt.Errorf("manifest must be an object")
	}
	for key, value := range keys {
		switch key {
		case "name", "short_name", "description", "lang", "dir", "id", "start_url", "scope", "display", "orientation", "background_color", "theme_color", "icons":
		default:
			return result, fmt.Errorf("unsupported manifest field")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return result, fmt.Errorf("manifest fields cannot be null")
		}
	}
	if raw, exists := keys["icons"]; exists {
		var icons []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &icons); err != nil {
			return result, fmt.Errorf("manifest icons must be objects")
		}
		for _, icon := range icons {
			if icon == nil {
				return result, fmt.Errorf("manifest icon cannot be null")
			}
			for key, value := range icon {
				switch key {
				case "src", "sizes", "type", "purpose":
				default:
					return result, fmt.Errorf("unsupported manifest icon field")
				}
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return result, fmt.Errorf("manifest icon fields cannot be null")
				}
			}
		}
	}
	return result, validateManifest(result, options)
}

func validateManifest(value Manifest, options Options) error {
	if err := (paths.Config{BasePath: options.BasePath}).Validate(); err != nil {
		return err
	}
	for _, field := range []struct {
		value string
		limit int
	}{{value.Name, 256}, {value.ShortName, 128}, {value.Description, 2048}, {value.Lang, 255}, {value.Dir, 8}, {value.ID, 8192}, {value.StartURL, 8192}, {value.Scope, 8192}, {value.Display, 32}, {value.Orientation, 32}, {value.BackgroundColor, 64}, {value.ThemeColor, 64}} {
		if len(field.value) > field.limit || !utf8.ValidString(field.value) || strings.IndexFunc(field.value, unicode.IsControl) >= 0 {
			return fmt.Errorf("manifest field exceeds text limits")
		}
	}
	if value.Lang != "" && !navigation.ValidLanguage(value.Lang) {
		return fmt.Errorf("invalid manifest language")
	}
	if value.Dir != "" && value.Dir != "ltr" && value.Dir != "rtl" && value.Dir != "auto" {
		return fmt.Errorf("invalid manifest direction")
	}
	if value.Display != "" && value.Display != "browser" && value.Display != "standalone" && value.Display != "minimal-ui" && value.Display != "fullscreen" {
		return fmt.Errorf("invalid manifest display")
	}
	switch value.Orientation {
	case "", "any", "natural", "landscape", "portrait", "portrait-primary", "portrait-secondary", "landscape-primary", "landscape-secondary":
	default:
		return fmt.Errorf("invalid manifest orientation")
	}
	if !manifestColor(value.ThemeColor) || !manifestColor(value.BackgroundColor) {
		return fmt.Errorf("unsupported manifest color")
	}
	start := value.StartURL
	if start == "" {
		start = "./"
	}
	startURL, err := manifestURL(start, options)
	if err != nil {
		return err
	}
	scope := value.Scope
	if scope == "" {
		scope = "./"
	}
	scopeURL, err := manifestURL(scope, options)
	if err != nil {
		return err
	}
	if scopeURL.RawQuery != "" || scopeURL.ForceQuery || scopeURL.Fragment != "" || !strings.HasSuffix(scopeURL.Path, "/") {
		return fmt.Errorf("manifest scope must be a local directory without query or fragment")
	}
	if !strings.HasPrefix(startURL.Path, scopeURL.Path) {
		return fmt.Errorf("manifest start URL is outside scope")
	}
	if value.ID != "" {
		id, err := manifestURL(value.ID, options)
		if err != nil || id.Fragment != "" {
			return fmt.Errorf("invalid manifest id")
		}
	}
	if len(value.Icons) > MaxManifestIcons {
		return fmt.Errorf("manifest exceeds %d icons", MaxManifestIcons)
	}
	seen := map[string]bool{}
	for _, icon := range value.Icons {
		if icon.Src == "" || len(icon.Src) > 8192 || len(icon.Sizes) > 128 || len(icon.Type) > 64 || len(icon.Purpose) > 64 {
			return fmt.Errorf("invalid manifest icon bounds")
		}
		for _, field := range []string{icon.Src, icon.Sizes, icon.Type, icon.Purpose} {
			if !utf8.ValidString(field) || strings.IndexFunc(field, unicode.IsControl) >= 0 {
				return fmt.Errorf("invalid manifest icon text")
			}
		}
		u, err := manifestURL(icon.Src, options)
		if err != nil || u.Fragment != "" {
			return fmt.Errorf("invalid manifest icon URL")
		}
		ext := strings.ToLower(path.Ext(u.Path))
		if ext != ".png" && ext != ".ico" {
			return fmt.Errorf("manifest icons support PNG and ICO only")
		}
		if icon.Type != "" && !(ext == ".png" && icon.Type == "image/png" || ext == ".ico" && (icon.Type == "image/x-icon" || icon.Type == "image/vnd.microsoft.icon")) {
			return fmt.Errorf("manifest icon type does not match its filename")
		}
		if err := manifestSizes(icon.Sizes); err != nil {
			return err
		}
		purposes := strings.Fields(icon.Purpose)
		if len(purposes) > 3 {
			return fmt.Errorf("invalid manifest icon purpose")
		}
		purposeSeen := map[string]bool{}
		for _, purpose := range purposes {
			if purpose != "any" && purpose != "maskable" && purpose != "monochrome" || purposeSeen[purpose] {
				return fmt.Errorf("invalid manifest icon purpose")
			}
			purposeSeen[purpose] = true
		}
		key := u.String()
		if seen[key] {
			return fmt.Errorf("duplicate manifest icon URL")
		}
		seen[key] = true
	}
	return nil
}

func manifestURL(value string, options Options) (*url.URL, error) {
	if value == "" || len(value) > 8192 || strings.Contains(value, "\\") || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fmt.Errorf("manifest URLs require bounded local paths")
	}
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Opaque != "" || strings.HasPrefix(value, "//") {
		return nil, fmt.Errorf("manifest URLs must be local")
	}
	// Resolve ordinary relative references against the manifest's public URL.
	// Check the result using the deployment URL validator, including decoded
	// traversal/control segments, before accepting its application scope.
	base := &url.URL{Scheme: "https", Host: "metadata.invalid", Path: options.BasePath + "/manifest.webmanifest"}
	resolved := base.ResolveReference(u)
	if _, err := (paths.Config{}).URL(resolved.RequestURI()); err != nil {
		return nil, fmt.Errorf("invalid manifest URL path")
	}
	if options.BasePath != "" && resolved.Path != options.BasePath && !strings.HasPrefix(resolved.Path, options.BasePath+"/") {
		return nil, fmt.Errorf("manifest URL escapes application mount")
	}
	return resolved, nil
}

func manifestSizes(value string) error {
	parts := strings.Fields(value)
	if len(parts) > 8 {
		return fmt.Errorf("too many manifest icon sizes")
	}
	seen := map[string]bool{}
	for _, part := range parts {
		dims := strings.Split(part, "x")
		if len(dims) != 2 || seen[part] {
			return fmt.Errorf("invalid manifest icon sizes")
		}
		seen[part] = true
		for _, dim := range dims {
			n, err := strconv.Atoi(dim)
			if err != nil || n < 1 || n > MaxImageDimension || strconv.Itoa(n) != dim {
				return fmt.Errorf("invalid manifest icon dimensions")
			}
		}
	}
	return nil
}

func manifestColor(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "#") {
		if len(value) != 4 && len(value) != 5 && len(value) != 7 && len(value) != 9 {
			return false
		}
		for _, r := range value[1:] {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
		return true
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}
