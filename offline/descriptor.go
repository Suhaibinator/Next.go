package offline

import (
	"strings"
	"unicode/utf8"
)

const (
	WorkerScriptPath          = "/_nextgo/offline/sw.js"
	CatalogPath               = "/_nextgo/offline/catalog"
	RegistrationHelperPath    = "/_nextgo/offline/registration-helper-v1"
	PreparedDocumentsPath     = "/_nextgo/offline/documents/"
	StorageNamespacePrefix    = "nextgo-offline-v1-"
	MaxClientDescriptorBytes  = 16 << 10
	ClientDescriptorAttribute = "data-nextgo-offline"
)

// ClientDescriptor is the lean public document-registration contract. It does
// not contain saved HTML, worker bootstrap bytes, cookies or native authority.
// The worker URL is nested; its native response must explicitly allow Scope.
type ClientDescriptor struct {
	Version         int    `json:"version"`
	Scope           string `json:"scope"`
	WorkerURL       string `json:"workerURL"`
	CatalogURL      string `json:"catalogURL"`
	OwnerPolicyID   string `json:"ownerPolicyID"`
	RuntimeID       string `json:"runtimeID"`
	ConfigurationID string `json:"configurationID"`
}

// WorkerDescriptor is embedded in the compiler bridge template only after the
// native public bundle is complete. CatalogJSON preserves the exact published
// bytes. Validation here binds those bytes to SHA256, but never interprets JSON;
// the native/worker strict codec must also parse it, construct NewCatalog and
// compare its generation, policy, runtime and configuration before use.
type WorkerDescriptor struct {
	Version       int              `json:"version"`
	Client        ClientDescriptor `json:"client"`
	Generation    string           `json:"generation"`
	CatalogJSON   string           `json:"catalogJSON"`
	CatalogSHA256 string           `json:"catalogSHA256"`
	Namespace     string           `json:"namespace"`
}

// StorageID is stable across runtime, configuration, budget and page-policy
// changes so upgrades cannot forget still-owned previous/staging storage. This
// is a public ownership key, not same-origin security isolation or a secret.
func (p *Policy) StorageID() string {
	if p == nil || p.config.ID == "" {
		return ""
	}
	return digest(p.owner())
}

func (p *Policy) StorageNamespace() string {
	id := p.StorageID()
	if id == "" {
		return ""
	}
	return StorageNamespacePrefix + id
}

// DocumentURL returns the exact canonical URL of a declared saved page. A
// mounted application root has no trailing slash, matching native routing.
func (p *Policy) DocumentURL(path string) (string, error) {
	if p == nil || p.config.ID == "" || !pagePath(path) {
		return "", ErrInvalid
	}
	for _, page := range p.config.Pages {
		if page.Path == path {
			return p.documentURL(path), nil
		}
	}
	return "", ErrInvalid
}

// ScopeURL includes the canonical mounted root itself. Browser scope matching
// is a prefix operation, so the worker must still bypass every URL outside its
// exact declared document/runtime inventory (including similarly named peers).
func (p *Policy) ScopeURL() string {
	if p == nil || p.config.ID == "" {
		return ""
	}
	return scopeURL(p.config.Origin, p.config.BasePath)
}

func scopeURL(origin, base string) string {
	if base == "" {
		return origin + "/"
	}
	return origin + base
}

// NormalizeClientDescriptor compares every value to trusted startup policy and
// expected compatibility identities. Request/DOM/forwarded values must not be
// used as those expected inputs. Every returned string is independently owned.
func NormalizeClientDescriptor(value ClientDescriptor, policy *Policy, runtimeID, configurationID string) (ClientDescriptor, error) {
	if policy == nil || !lowerHex(runtimeID, 20) || !lowerHex(configurationID, 64) {
		return ClientDescriptor{}, ErrInvalid
	}
	p, err := Normalize(policy.Config())
	if err != nil {
		return ClientDescriptor{}, err
	}
	if err := ValidateClientDescriptor(value, p.config.Origin, p.config.BasePath, runtimeID, configurationID); err != nil {
		return ClientDescriptor{}, err
	}
	if value.OwnerPolicyID != p.config.ID {
		return ClientDescriptor{}, ErrInvalid
	}
	value.Scope, value.WorkerURL, value.CatalogURL = strings.Clone(value.Scope), strings.Clone(value.WorkerURL), strings.Clone(value.CatalogURL)
	value.OwnerPolicyID, value.RuntimeID, value.ConfigurationID = strings.Clone(value.OwnerPolicyID), strings.Clone(value.RuntimeID), strings.Clone(value.ConfigurationID)
	return value, nil
}

// ValidateClientDescriptor is the document adapter's policy-free geometry and
// compatibility check. Origin/base/runtime/configuration must come from the
// current trusted document environment, not from the raw descriptor itself.
// The native Normalize helper additionally checks the configured owner ID.
// Parse raw JSON through the separate strict codec before calling this helper;
// the portable policy intentionally has no encoding/json dependency.
func ValidateClientDescriptor(value ClientDescriptor, origin, base, runtimeID, configurationID string) error {
	if !literalOrigin(origin) || !basePath(base) || !lowerHex(runtimeID, 20) || !lowerHex(configurationID, 64) || !identifier(value.OwnerPolicyID, 64) {
		return ErrInvalid
	}
	root := origin + base
	if value.Version != Version || value.Scope != scopeURL(origin, base) || value.WorkerURL != root+WorkerScriptPath || value.CatalogURL != root+CatalogPath || value.RuntimeID != runtimeID || value.ConfigurationID != configurationID {
		return ErrInvalid
	}
	for _, field := range []string{value.Scope, value.WorkerURL, value.CatalogURL} {
		if len(field) > MaxURLBytes {
			return ErrInvalid
		}
	}
	return nil
}

func NormalizeWorkerDescriptor(value WorkerDescriptor, policy *Policy, runtimeID, configurationID string) (WorkerDescriptor, error) {
	client, err := NormalizeClientDescriptor(value.Client, policy, runtimeID, configurationID)
	if err != nil {
		return WorkerDescriptor{}, err
	}
	if value.Version != Version || !lowerHex(value.Generation, 64) || !lowerHex(value.CatalogSHA256, 64) || len(value.CatalogJSON) == 0 || len(value.CatalogJSON) > MaxCatalogBytes || !utf8.ValidString(value.CatalogJSON) || digest(value.CatalogJSON) != value.CatalogSHA256 || value.Namespace != policy.StorageNamespace() {
		return WorkerDescriptor{}, ErrInvalid
	}
	value.Client = client
	value.Generation, value.CatalogJSON = strings.Clone(value.Generation), strings.Clone(value.CatalogJSON)
	value.CatalogSHA256, value.Namespace = strings.Clone(value.CatalogSHA256), strings.Clone(value.Namespace)
	return value, nil
}

// RegistrationHelperURL derives the one inert document URL from a canonical
// worker URL. It validates URL/CSP-source syntax, not registration authority:
// callers must still bind the descriptor to their trusted origin and policy.
func RegistrationHelperURL(workerURL string) (string, error) {
	if len(workerURL) == 0 || len(workerURL) > MaxURLBytes || !strings.HasSuffix(workerURL, WorkerScriptPath) {
		return "", ErrInvalid
	}
	// The result is also used next to the exact worker URL in a CSP policy.
	// Reject separators, quoting, wildcard/query/fragment and percent aliases
	// explicitly rather than treating an arbitrary descriptor as a policy token.
	for _, c := range workerURL {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(":/.-_~", c)) {
			return "", ErrInvalid
		}
	}
	root := strings.TrimSuffix(workerURL, WorkerScriptPath)
	scheme := strings.Index(root, "://")
	if scheme < 0 {
		return "", ErrInvalid
	}
	origin, base := root, ""
	if offset := strings.IndexByte(root[scheme+3:], '/'); offset >= 0 {
		offset += scheme + 3
		origin, base = root[:offset], root[offset:]
	}
	if !literalOrigin(origin) || !basePath(base) || workerURL != origin+base+WorkerScriptPath {
		return "", ErrInvalid
	}
	// A CSP host-source is narrower than a URL authority: bracket IPv6 was
	// rejected above, and only the explicitly supported loopback IP source is
	// admitted. Other canonical offline origins retain their ordinary APIs but
	// fail closed on this optional helper instead of emitting a nonmatching CSP.
	host, _, _ := strings.Cut(origin[scheme+3:], ":")
	numeric := true
	for _, c := range host {
		numeric = numeric && (c >= '0' && c <= '9' || c == '.')
	}
	if numeric && host != "127.0.0.1" {
		return "", ErrInvalid
	}
	value := origin + base + RegistrationHelperPath
	if len(value) > MaxURLBytes {
		return "", ErrInvalid
	}
	return value, nil
}
