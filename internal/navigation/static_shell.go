package navigation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const StaticShellVersion = 1
const MaxStaticShellDescriptorBytes = 16 << 10
const MaxStaticShellEnvelopeBytes = 8 << 20

// StaticShellDescriptor binds an explicitly public, immutable seed envelope to
// one optional-catchall page. It is a compatibility/integrity check, never an
// authentication credential or proof of path-specific native rendering.
type StaticShellDescriptor struct {
	Version       int    `json:"version"`
	Build         string `json:"build"`
	Configuration string `json:"configuration"`
	Pattern       string `json:"pattern"`
	Component     string `json:"component"`
	SeedURL       string `json:"seedURL"`
	EnvelopeURL   string `json:"envelopeURL"`
	SHA256        string `json:"sha256"`
	Bytes         int    `json:"bytes"`
}

// ValidateStaticShellDeclaration returns the escaped, unmounted zero-tail seed
// path. Native callers must additionally prove exclusivity using their actual
// effective routes, handlers, document paths and export policies.
func ValidateStaticShellDeclaration(pattern, component string) (seedPath string, err error) {
	if len(pattern) == 0 || len(pattern) > 8192 || !utf8.ValidString(pattern) || len(component) == 0 || len(component) > 256 || !utf8.ValidString(component) || strings.IndexFunc(component, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid static shell declaration bounds")
	}
	if _, err := routing.Validate(pattern); err != nil {
		return "", fmt.Errorf("invalid static shell pattern: %w", err)
	}
	cut := strings.LastIndexByte(pattern, '/')
	if cut <= 0 || !strings.HasPrefix(pattern[cut+1:], "*") || !strings.HasSuffix(pattern, "?") {
		return "", fmt.Errorf("static shell requires a literal nonempty prefix and terminal optional catchall")
	}
	prefix := pattern[:cut]
	for _, segment := range strings.Split(prefix[1:], "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			return "", fmt.Errorf("static shell prefix must be literal")
		}
	}
	first, _, _ := strings.Cut(prefix[1:], "/")
	switch first {
	case "api", "_nextgo", "assets", ".well-known":
		return "", fmt.Errorf("static shell prefix overlaps a reserved namespace")
	}
	seedPath, err = routing.Build(prefix, nil)
	if err != nil || len(seedPath) > 8192 {
		return "", fmt.Errorf("invalid static shell seed path")
	}
	if !StaticShellDocumentPath(seedPath) {
		return "", fmt.Errorf("static shell seed has a reserved resource suffix")
	}
	return seedPath, nil
}

// StaticShellDocumentPath applies the experimental static-shell-only document
// restriction. It validates an escaped path and examines the final decoded
// segment exactly once, after removing at most one optional terminal slash.
// Exact retained files and ordinary exported documents are handled before a
// host considers virtual shell fallback. This predicate does not establish
// subtree ownership, an HTTP method or MIME type. Native HTTP, regular static
// documents and general RouteParams matching retain their existing behavior.
func StaticShellDocumentPath(escapedPath string) bool {
	params, ok := MatchRouteParams("/*nextgo_static_shell_path?", "", escapedPath)
	if !ok {
		return false
	}
	decoded := params["nextgo_static_shell_path"]
	final := decoded[strings.LastIndexByte(decoded, '/')+1:]
	final = strings.ToLower(final)
	for _, suffix := range []string{".json", ".wasm", ".css", ".js", ".mjs", ".cjs", ".map"} {
		if strings.HasSuffix(final, suffix) {
			return false
		}
	}
	return true
}

// StaticShellEnvelopeURL is always application-mounted and same-origin, even
// when runtime assets use a remote AssetPrefix.
func StaticShellEnvelopeURL(deployment paths.Config, build, digest string) (string, error) {
	if err := deployment.Validate(); err != nil {
		return "", err
	}
	if !staticShellBuildID(build) || !ascii.LowerHex(digest, sha256.Size*2) {
		return "", fmt.Errorf("invalid static shell envelope identity")
	}
	return deployment.BasePath + "/_nextgo/" + build + "/static-shell/" + digest + ".json", nil
}

// Validate checks identity and canonical derived URLs, including serialized
// descriptor size. Callers still validate export-only use and envelope content.
func (d StaticShellDescriptor) Validate(deployment paths.Config, build, configuration string) error {
	if d.Version != StaticShellVersion || d.Build != build || !staticShellBuildID(build) || d.Configuration != configuration || !ValidConfigurationID(configuration) || !ascii.LowerHex(d.SHA256, sha256.Size*2) || d.Bytes < 1 || d.Bytes > MaxStaticShellEnvelopeBytes {
		return fmt.Errorf("invalid static shell descriptor identity or bounds")
	}
	seed, err := ValidateStaticShellDeclaration(d.Pattern, d.Component)
	if err != nil {
		return err
	}
	expectedSeed, err := deployment.URL(seed)
	if err != nil || d.SeedURL != expectedSeed {
		return fmt.Errorf("invalid static shell seed URL")
	}
	expectedEnvelope, err := StaticShellEnvelopeURL(deployment, build, d.SHA256)
	if err != nil || d.EnvelopeURL != expectedEnvelope {
		return fmt.Errorf("invalid static shell envelope URL")
	}
	data, err := json.Marshal(d)
	if err != nil || len(data) > MaxStaticShellDescriptorBytes {
		return fmt.Errorf("static shell descriptor exceeds bounds")
	}
	return nil
}

func EncodeStaticShellDescriptor(d StaticShellDescriptor, deployment paths.Config, build, configuration string) ([]byte, error) {
	if err := d.Validate(deployment, build, configuration); err != nil {
		return nil, err
	}
	return json.Marshal(d)
}

func DecodeStaticShellDescriptor(data []byte, deployment paths.Config, build, configuration string) (StaticShellDescriptor, error) {
	var d StaticShellDescriptor
	if len(data) == 0 || len(data) > MaxStaticShellDescriptorBytes || !utf8.Valid(data) || wirejson.Unique(data) != nil {
		return d, fmt.Errorf("invalid static shell descriptor JSON")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil || len(fields) != 9 {
		return d, fmt.Errorf("invalid static shell descriptor object")
	}
	for name, raw := range fields {
		switch name {
		case "version", "bytes":
			var value *int
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return d, fmt.Errorf("invalid static shell integer")
			}
		case "build", "configuration", "pattern", "component", "seedURL", "envelopeURL", "sha256":
			var value *string
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return d, fmt.Errorf("invalid static shell string")
			}
		default:
			return d, fmt.Errorf("unknown static shell descriptor field")
		}
	}
	if !bytes.Equal(bytes.TrimSpace(fields["version"]), []byte("1")) || wirejson.Strict(data, &d) != nil {
		return StaticShellDescriptor{}, fmt.Errorf("unsupported static shell descriptor version or shape")
	}
	if err := d.Validate(deployment, build, configuration); err != nil {
		return StaticShellDescriptor{}, err
	}
	return d, nil
}

// CheckEnvelope hashes the original bytes before any local URL rebinding.
// Normal navigation protocol, status, document and seed-URL checks remain the
// caller's responsibility. Integrity is not an authorization boundary.
func (d StaticShellDescriptor) CheckEnvelope(data []byte) error {
	if d.Bytes < 1 || d.Bytes > MaxStaticShellEnvelopeBytes || len(data) != d.Bytes || !ascii.LowerHex(d.SHA256, sha256.Size*2) {
		return fmt.Errorf("static shell envelope byte count mismatch")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != d.SHA256 {
		return fmt.Errorf("static shell envelope digest mismatch")
	}
	return nil
}

// MatchTarget admits only the declared page when it is the actual first
// matching manifest route. It never turns a missing ordinary page into a shell.
// A descriptor must pass Validate before its first use.
func (d StaticShellDescriptor) MatchTarget(escapedPath string, deployment paths.Config, manifest Manifest) (map[string]string, bool) {
	if !StaticShellDocumentPath(escapedPath) {
		return nil, false
	}
	seed, err := ValidateStaticShellDeclaration(d.Pattern, d.Component)
	if err != nil || deployment.Validate() != nil {
		return nil, false
	}
	params, ok := MatchRouteParams(d.Pattern, deployment.BasePath, escapedPath)
	if !ok {
		return nil, false
	}
	local, _ := deployment.Strip(escapedPath)
	logical := paths.Logical(local)
	// Literal prefix spellings cannot gain ownership through percent aliases.
	if logical != seed && !strings.HasPrefix(logical, seed+"/") {
		return nil, false
	}
	decoded, err := url.PathUnescape(local)
	if err != nil || manifest.IsEndpoint(local, decoded) {
		return nil, false
	}
	first, ok := FirstMatchingRoute(manifest, local)
	if !ok || first.Endpoint || first.Pattern != d.Pattern {
		return nil, false
	}
	return params, true
}

// MatchRouteParams is portable Go and returns a newly owned decoded map. It
// matches a caller-provided pattern; it is not active-route authorization.
func MatchRouteParams(pattern, basePath, escapedPath string) (map[string]string, bool) {
	if len(pattern) == 0 || len(pattern) > 8192 || len(basePath) > 8192 || len(escapedPath) == 0 || len(escapedPath) > 8192 || !utf8.ValidString(pattern) || !utf8.ValidString(basePath) || !utf8.ValidString(escapedPath) || (paths.Config{BasePath: basePath}).Validate() != nil {
		return nil, false
	}
	local, ok := paths.Strip(basePath, escapedPath)
	if !ok {
		return nil, false
	}
	params, matched := routing.Match(pattern, paths.Logical(local))
	if !matched {
		return nil, false
	}
	return params, true
}

// FirstMatchingRoute preserves the manifest's existing specificity order.
func FirstMatchingRoute(manifest Manifest, escapedApplicationPath string) (Route, bool) {
	if _, ok := MatchRouteParams("/*nextgo_path?", "", escapedApplicationPath); !ok {
		return Route{}, false
	}
	for _, route := range manifest.Routes {
		if _, ok := MatchRouteParams(route.Pattern, "", escapedApplicationPath); ok {
			return route, true
		}
	}
	return Route{}, false
}

func staticShellBuildID(value string) bool {
	// Descriptor v1 supports paired CLI-generated builds only. Config.BuildID
	// otherwise has no shared general syntax validator; manually supplied IDs
	// remain valid for native HTTP and ordinary exports under their prior rules.
	return ascii.LowerHex(value, 20)
}
