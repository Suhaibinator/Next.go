// Package devrefresh defines the bounded development-only module refresh wire
// contract. Production navigation and the component host ABI are independent.
// Manifests contain public descriptors only; state snapshots must stay in memory
// and must never be sent over HTTP/SSE, logged, or written to browser storage.
package devrefresh

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const (
	Version          = 1
	ExtensionVersion = 1
	ModuleABI        = 6
	// ShimDigest is the exact paired shim qualified by the standalone Go 1.24.13
	// and 1.27.3 retirement probes. Unknown shims require ordinary document reload.
	ShimDigest = "0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14"

	EventName    = "refresh"
	ManifestPath = "/_nextgo/refresh"

	MaxModules         = 8
	MaxScopesPerModule = 64
	MaxCellsPerModule  = 64
	MaxCellBytes       = 16 << 10
	MaxStateBytes      = 256 << 10
	MaxModuleBytes     = 8 << 20
	MaxManifestBytes   = 32 << 10
	MaxSwaps           = 16
	MaxTokenBytes      = 128
	// Revisions and cross-runtime handles must round-trip exactly through JS.
	MaxRevision = uint64(1<<53 - 1)
	Watchdog    = 5 * time.Second

	MaxScopes        = MaxScopesPerModule
	MaxCells         = MaxCellsPerModule
	MaxSnapshotBytes = MaxStateBytes
)

var (
	ErrInvalid      = errors.New("devrefresh: invalid payload")
	ErrIncompatible = errors.New("devrefresh: incompatible refresh")
	ErrRevision     = errors.New("devrefresh: revision chain changed")
)

// Module is a public content-addressed candidate. URL is a canonical local path
// served by the development supervisor, never an arbitrary external recipient.
type Module struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Digest      string `json:"digest"`
	Size        int    `json:"size"`
	Epoch       string `json:"epoch"`
	StateSchema string `json:"stateSchema"`
	DOMSchema   string `json:"domSchema"`
}

// Manifest is a complete opted-in module table, bound to a still-running native
// child/core. Revision zero is the baseline. Eligible updates advance exactly
// one revision; fresh documents may bootstrap directly from the latest table.
type Manifest struct {
	Version           int      `json:"version"`
	Revision          uint64   `json:"revision"`
	Previous          uint64   `json:"previous"`
	ServingBuild      string   `json:"servingBuild"`
	ServingGeneration string   `json:"servingGeneration"`
	Compatibility     string   `json:"compatibility"`
	ShimDigest        string   `json:"shimDigest"`
	ModuleABI         int      `json:"moduleABI"`
	Modules           []Module `json:"modules"`
}

// ValidToken accepts bounded public schema, instance and state-key identities.
// Identity is exact and case-sensitive; no normalization or positional matching
// can silently migrate state to a different key.
func ValidToken(value string) bool {
	if len(value) == 0 || len(value) > MaxTokenBytes || !alphaNumeric(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		if !alphaNumeric(value[i]) && !strings.ContainsRune("._:/-", rune(value[i])) {
			return false
		}
	}
	return true
}

func alphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func validModuleName(value string) bool {
	if !ValidToken(value) || strings.Contains(value, ":") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (m Module) Validate() error {
	if !validModuleName(m.Name) || !ascii.LowerHex(m.Digest, 64) || !ascii.LowerHex(m.Epoch, 64) || m.Size < 1 || m.Size > MaxModuleBytes || !ValidToken(m.StateSchema) || !ValidToken(m.DOMSchema) || !validModuleURL(m.URL, m.Digest) {
		return ErrInvalid
	}
	return nil
}

func validModuleURL(value, digest string) bool {
	if len(value) > 8192 || !strings.HasPrefix(value, "/") || strings.Contains(value, "//") || strings.ContainsAny(value, "%\\?#\r\n\t") || path.Clean(value) != value {
		return false
	}
	for i := 1; i < len(value); i++ {
		if !alphaNumeric(value[i]) && !strings.ContainsRune("/-._~", rune(value[i])) {
			return false
		}
	}
	parts := strings.Split(value, "/")
	n := len(parts)
	return n >= 4 && parts[n-3] == "_nextgo" && ascii.LowerHex(parts[n-2], 20) && parts[n-1] == "module-"+digest+".wasm"
}

func (m Manifest) Validate() error {
	if m.Version != Version || m.Revision > MaxRevision || m.Previous > MaxRevision || m.Revision == 0 && m.Previous != 0 || m.Revision > 0 && m.Previous != m.Revision-1 || !ascii.LowerHex(m.ServingBuild, 20) || !ascii.LowerHex(m.ServingGeneration, 32) || !ascii.LowerHex(m.Compatibility, 64) || m.ShimDigest != ShimDigest || m.ModuleABI != ModuleABI || len(m.Modules) == 0 || len(m.Modules) > MaxModules {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(m.Modules))
	for _, module := range m.Modules {
		if module.Validate() != nil || seen[module.Name] {
			return ErrInvalid
		}
		seen[module.Name] = true
	}
	data, err := json.Marshal(m)
	if err != nil || len(data) > MaxManifestBytes {
		return ErrInvalid
	}
	return nil
}

// Next checks both the live revision chain and invariant native/core/module
// ownership. URL/digest/size/epoch are the only mutable descriptor fields.
// It does not prove compiler graph compatibility; Compatibility must already
// have been derived from the actual selected native/core inputs by the compiler.
func (m Manifest) Next(previous Manifest) error {
	if m.Validate() != nil || previous.Validate() != nil {
		return ErrInvalid
	}
	if previous.Revision == MaxRevision || m.Revision != previous.Revision+1 || m.Previous != previous.Revision {
		return ErrRevision
	}
	if m.ServingBuild != previous.ServingBuild || m.ServingGeneration != previous.ServingGeneration || m.Compatibility != previous.Compatibility || m.ShimDigest != previous.ShimDigest || m.ModuleABI != previous.ModuleABI || len(m.Modules) != len(previous.Modules) {
		return ErrIncompatible
	}
	old := make(map[string]Module, len(previous.Modules))
	for _, module := range previous.Modules {
		old[module.Name] = module
	}
	for _, module := range m.Modules {
		before, ok := old[module.Name]
		if !ok || module.StateSchema != before.StateSchema || module.DOMSchema != before.DOMSchema {
			return ErrIncompatible
		}
	}
	return nil
}

func ParseManifest(data []byte) (Manifest, error) {
	var value Manifest
	if validJSON(data, MaxManifestBytes) != nil {
		return value, ErrInvalid
	}
	object, err := exactObject(data, "version", "revision", "previous", "servingBuild", "servingGeneration", "compatibility", "shimDigest", "moduleABI", "modules")
	if err != nil || exactArray(object["modules"], moduleKeys, MaxModules) != nil || wirejson.Strict(data, &value) != nil || value.Validate() != nil {
		return Manifest{}, ErrInvalid
	}
	return value, nil
}

var moduleKeys = []string{"name", "url", "digest", "size", "epoch", "stateSchema", "domSchema"}

func validJSON(data []byte, limit int) error {
	if len(data) == 0 || len(data) > limit || !utf8.Valid(data) || wirejson.Unique(data) != nil {
		return ErrInvalid
	}
	return nil
}

// Exact keys prevent encoding/json's case-insensitive field matching from
// admitting a second spelling of an otherwise unique wire field.
func exactObject(data []byte, keys ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != len(keys) {
		return nil, ErrInvalid
	}
	for _, key := range keys {
		value, ok := object[key]
		if !ok || key != "value" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrInvalid
		}
	}
	return object, nil
}

func exactArray(data []byte, keys []string, limit int) error {
	var values []json.RawMessage
	if string(data) == "null" || json.Unmarshal(data, &values) != nil || values == nil || len(values) > limit {
		return ErrInvalid
	}
	for _, value := range values {
		if _, err := exactObject(value, keys...); err != nil {
			return err
		}
	}
	return nil
}
