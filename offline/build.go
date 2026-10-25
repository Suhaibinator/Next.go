//go:build !js

package offline

import (
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxBridgeBytes = 256 << 10
	MaxBuildBytes  = 128 << 20
	// BuildDescriptorPlaceholder appears exactly once in the compiler-owned
	// bridge template. Native preparation replaces it with the complete bounded
	// public bootstrap descriptor only after its document bundle is approved.
	BuildDescriptorPlaceholder = "__NEXTGO_OFFLINE_DESCRIPTOR__"
)

// BuildFile describes exact decoded bytes relative to Config.Assets. Path is
// _nextgo/<RuntimeID>/..., including offline-public/ for frozen public files.
// The descriptor has no filesystem, origin, request or application capability.
type BuildFile struct {
	Path         string
	Kind         AssetKind
	SHA256       string
	DecodedBytes int64
	ContentType  string
}

// Build is the native compiler descriptor, separate from the final Catalog.
// The compiler hashes logical relative snapshots, worker/shim and bridge
// TEMPLATE before assigning RuntimeID, then fills the final relative paths.
// Neither the final catalog generation nor its origin/documents feeds back
// into RuntimeID. Producers must not mutate this value during normalization.
type Build struct {
	Version        int
	RuntimeID      string
	Files          []BuildFile
	BridgeTemplate string
}

// NormalizeBuild validates the complete required inventory without executing
// application code or reading a filesystem. The return value owns all fields;
// native startup must additionally read and verify every actual source byte.
func NormalizeBuild(build Build) (Build, error) {
	if build.Version != Version || !lowerHex(build.RuntimeID, 20) || len(build.Files) == 0 || len(build.Files) > MaxAssets || len(build.BridgeTemplate) == 0 || len(build.BridgeTemplate) > MaxBridgeBytes || !utf8.ValidString(build.BridgeTemplate) || strings.ContainsRune(build.BridgeTemplate, 0) || strings.Count(build.BridgeTemplate, BuildDescriptorPlaceholder) != 1 {
		return Build{}, ErrInvalid
	}
	// Reuse the catalog's role/path/MIME validation without inventing a public
	// origin. The synthetic value is never returned or used for network I/O.
	synthetic := &Policy{config: Config{Origin: "https://offline.invalid", Budget: Budget{AssetBytes: 32 << 20}}}
	files := make([]BuildFile, len(build.Files))
	counts := map[AssetKind]int{}
	var bytes int64
	for i, file := range build.Files {
		if len(file.Path) > MaxURLBytes || !strings.HasPrefix(file.Path, "_nextgo/"+build.RuntimeID+"/") || !literalPath("/"+file.Path, false, false) || !assetValid(synthetic, build.RuntimeID, Asset{Kind: file.Kind, URL: synthetic.config.Origin + "/" + file.Path, SHA256: file.SHA256, DecodedBytes: file.DecodedBytes, ContentType: file.ContentType}) {
			return Build{}, ErrInvalid
		}
		// The ordinary immutable-asset dispatcher also denies these segments.
		// Refuse an inventory which would require an unservable source path.
		for _, segment := range strings.Split(file.Path, "/") {
			lower := strings.ToLower(segment)
			if lower == ".env" || strings.HasPrefix(lower, ".env.") {
				return Build{}, ErrInvalid
			}
		}
		bytes += file.DecodedBytes
		if bytes > MaxBuildBytes {
			return Build{}, ErrQuota
		}
		counts[file.Kind]++
		files[i] = cloneBuildFile(file)
	}
	for _, kind := range []AssetKind{WorkerWASM, CoreWASM, RuntimeShim, BootScript} {
		if counts[kind] != 1 {
			return Build{}, ErrIncomplete
		}
	}
	if counts[Style] == 0 {
		return Build{}, ErrIncomplete
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for i := 1; i < len(files); i++ {
		if files[i-1].Path == files[i].Path {
			return Build{}, ErrInvalid
		}
	}
	return Build{Version: build.Version, RuntimeID: strings.Clone(build.RuntimeID), Files: files, BridgeTemplate: strings.Clone(build.BridgeTemplate)}, nil
}

func cloneBuildFile(file BuildFile) BuildFile {
	file.Path, file.SHA256, file.ContentType = strings.Clone(file.Path), strings.Clone(file.SHA256), strings.Clone(file.ContentType)
	file.Kind = AssetKind(strings.Clone(string(file.Kind)))
	return file
}

// Clone copies the descriptor without validating it. Use NormalizeBuild at a
// trust boundary before storing or publishing a compiler-provided descriptor.
func (build Build) Clone() Build {
	build.RuntimeID, build.BridgeTemplate = strings.Clone(build.RuntimeID), strings.Clone(build.BridgeTemplate)
	files := make([]BuildFile, len(build.Files))
	for i, file := range build.Files {
		files[i] = cloneBuildFile(file)
	}
	build.Files = files
	return build
}

// Assets binds owned compiler records to the trusted startup policy's exact
// origin and mount. It never reads bytes, contacts that origin, adds a mutable
// /assets alias, or raises an explicitly smaller policy budget.
func (build Build) Assets(policy *Policy) ([]Asset, error) {
	if policy == nil {
		return nil, ErrInvalid
	}
	p, err := Normalize(policy.Config())
	if err != nil {
		return nil, err
	}
	normalized, err := NormalizeBuild(build)
	if err != nil {
		return nil, err
	}
	if len(normalized.Files)+len(p.config.Pages)+2 > p.config.Budget.GenerationEntries || len(normalized.Files)+len(p.config.Pages)+2 > p.config.Budget.Entries {
		return nil, ErrQuota
	}
	assets := make([]Asset, len(normalized.Files))
	bytes := int64(MaxCatalogBytes + MaxCommitBytes)
	for i, file := range normalized.Files {
		asset := Asset{Kind: file.Kind, URL: p.config.Origin + p.config.BasePath + "/" + file.Path, SHA256: file.SHA256, DecodedBytes: file.DecodedBytes, ContentType: file.ContentType}
		if !assetValid(p, normalized.RuntimeID, asset) {
			return nil, ErrInvalid
		}
		bytes += asset.DecodedBytes
		if bytes > p.config.Budget.GenerationBytes || bytes > p.config.Budget.TotalBytes {
			return nil, ErrQuota
		}
		assets[i] = asset
	}
	return assets, nil
}
