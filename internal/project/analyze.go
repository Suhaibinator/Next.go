package project

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Analysis describes a completed ordinary application build, never a prediction
// from filenames. It deliberately has no configuration, props or source paths.
type Analysis struct {
	Version     int                 `json:"version"`
	AnalyzerGo  string              `json:"analyzerGo"`
	Compression string              `json:"compression"`
	Scope       string              `json:"scope"`
	Build       string              `json:"build"`
	Fingerprint string              `json:"fingerprint"`
	Native      NativeAnalysis      `json:"native"`
	Assets      []AssetAnalysis     `json:"assets"`
	Components  []ComponentAnalysis `json:"components"`
	Routes      []RouteAnalysis     `json:"routes"`
	Totals      AnalysisTotals      `json:"totals"`
	Budgets     AnalysisBudgets     `json:"budgets"`
	Violations  []BudgetViolation   `json:"violations"`
}

type NativeAnalysis struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Go     string `json:"go"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	CGO    string `json:"cgo"`
}

type AssetAnalysis struct {
	File       string        `json:"file"` // Relative to the immutable runtime namespace.
	Kind       string        `json:"kind"`
	Bytes      int64         `json:"bytes"`
	Gzip9Bytes int64         `json:"gzip9Bytes"`
	SHA256     string        `json:"sha256"`
	Sections   []WASMSection `json:"sections,omitempty"`
}

type ComponentAnalysis struct {
	Name    string `json:"name"`
	Loading string `json:"loading"`
	File    string `json:"file"`
}

type RouteAnalysis struct {
	Pattern   string `json:"pattern"`
	Kind      string `json:"kind"`
	Layouts   int    `json:"layouts"`
	Templates int    `json:"templates"`
	Slots     int    `json:"slots"`
}

type AnalysisTotals struct {
	AllBytes        int64 `json:"allBytes"`
	AllGzip9Bytes   int64 `json:"allGzip9Bytes"`
	EagerBytes      int64 `json:"eagerBytes"`
	EagerGzip9Bytes int64 `json:"eagerGzip9Bytes"`
}

// Zero disables a limit. These are application-selected file byte budgets,
// not framework-approved performance or HTTP transfer guarantees.
type AnalysisBudgets struct {
	CoreBytes      int64 `json:"coreBytes"`
	CoreGzip9Bytes int64 `json:"coreGzip9Bytes"`
	LazyBytes      int64 `json:"lazyBytes"`
	LazyGzip9Bytes int64 `json:"lazyGzip9Bytes"`
}

func (b AnalysisBudgets) Validate() error {
	if b.CoreBytes < 0 || b.CoreGzip9Bytes < 0 || b.LazyBytes < 0 || b.LazyGzip9Bytes < 0 {
		return fmt.Errorf("analysis byte budgets must be nonnegative; zero disables a limit")
	}
	return nil
}

type BudgetViolation struct {
	File   string `json:"file"`
	Metric string `json:"metric"`
	Actual int64  `json:"actual"`
	Limit  int64  `json:"limit"`
}

// AnalyzeBuild reads only the successful generation's runtime files and native
// executable. It does not run the executable or rediscover application files.
// The caller still owns/removes the compiled generation.
func AnalyzeBuild(ctx context.Context, b *Build, budgets AnalysisBudgets) (*Analysis, error) {
	if err := budgets.Validate(); err != nil {
		return nil, err
	}
	if b == nil || b.Project == nil || !analysisBuildID(b.ID) {
		return nil, fmt.Errorf("analysis requires a completed ordinary application build")
	}
	result := &Analysis{Version: 1, AnalyzerGo: runtime.Version(), Compression: "go-compress-gzip-level-9",
		Scope: "shared runtime assets and static route summaries; route component usage is not inferred",
		Build: b.ID, Fingerprint: b.Fingerprint, Budgets: budgets,
		Assets: []AssetAnalysis{}, Components: []ComponentAnalysis{}, Routes: []RouteAnalysis{}, Violations: []BudgetViolation{}}
	root := filepath.Join(b.Dir, "server", "assets", "_nextgo", b.ID)
	assets, err := analyzeRuntime(ctx, os.DirFS(root))
	if err != nil {
		return nil, err
	}
	result.Assets = assets
	byFile := make(map[string]string, len(assets))
	for _, asset := range assets {
		byFile[asset.File] = asset.Kind
		result.Totals.AllBytes += asset.Bytes
		result.Totals.AllGzip9Bytes += asset.Gzip9Bytes
		if asset.Kind == "core" || asset.Kind == "bootstrap" || asset.Kind == "shim" {
			result.Totals.EagerBytes += asset.Bytes
			result.Totals.EagerGzip9Bytes += asset.Gzip9Bytes
		}
		result.Violations = append(result.Violations, assetViolations(asset, budgets)...)
	}
	modules := make(map[string]string, len(b.RuntimeModules))
	for _, module := range b.RuntimeModules {
		if byFile[module.File] != "lazy" || modules[module.Component] != "" {
			return nil, fmt.Errorf("analysis lazy registration does not match compiled assets")
		}
		modules[module.Component] = module.File
	}
	for _, component := range b.Project.Components {
		entry := ComponentAnalysis{Name: component.Name, Loading: "eager", File: "app.wasm"}
		if component.Lazy {
			entry.Loading, entry.File = "lazy", modules[component.Name]
			if entry.File == "" {
				return nil, fmt.Errorf("analysis is missing a compiled lazy registration")
			}
		}
		result.Components = append(result.Components, entry)
	}
	sort.Slice(result.Components, func(i, j int) bool { return result.Components[i].Name < result.Components[j].Name })
	for _, route := range b.Project.Routes {
		kind := "page"
		if route.API {
			kind = "api"
		}
		result.Routes = append(result.Routes, RouteAnalysis{route.Pattern, kind, len(route.Layouts), len(route.Templates), len(route.Slots)})
	}
	sort.Slice(result.Routes, func(i, j int) bool { return result.Routes[i].Pattern < result.Routes[j].Pattern })
	result.Native, err = analyzeNative(ctx, b.Binary)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func analysisBuildID(id string) bool {
	if len(id) != 20 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func analyzeRuntime(ctx context.Context, files fs.FS) ([]AssetAnalysis, error) {
	var result []AssetAnalysis
	var total int64
	entries := 0
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 1024 || entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("analysis runtime entry bound or symlink violation")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 64<<20 {
			return fmt.Errorf("analysis requires regular runtime files of at most 64 MiB")
		}
		total += info.Size()
		if total > 256<<20 {
			return fmt.Errorf("analysis runtime exceeds 256 MiB")
		}
		file, err := files.Open(path)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(analysisReader{ctx, file}, info.Size()+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) != info.Size() {
			return fmt.Errorf("analysis runtime file changed while reading")
		}
		asset, err := analyzeAsset(ctx, path, data)
		if err != nil {
			return err
		}
		result = append(result, asset)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, required := range []string{"app.wasm", "boot.js", "wasm_exec.js"} {
		found := false
		for _, asset := range result {
			found = found || asset.File == required
		}
		if !found {
			return nil, fmt.Errorf("analysis is missing runtime asset %s", required)
		}
	}
	return result, nil
}

type analysisReader struct {
	ctx context.Context
	io.Reader
}

func (r analysisReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(data)
}

type analysisCounter struct {
	ctx   context.Context
	bytes int64
}

func (c *analysisCounter) Write(data []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	c.bytes += int64(len(data))
	return len(data), nil
}

func analyzeAsset(ctx context.Context, name string, data []byte) (AssetAnalysis, error) {
	asset := AssetAnalysis{File: name, Bytes: int64(len(data)), Kind: "auxiliary"}
	switch {
	case name == "app.wasm":
		asset.Kind = "core"
	case name == "boot.js":
		asset.Kind = "bootstrap"
	case name == "wasm_exec.js":
		asset.Kind = "shim"
	case strings.HasPrefix(name, "module-") && strings.HasSuffix(name, ".wasm"):
		asset.Kind = "lazy"
	case strings.HasPrefix(name, "worker-") && strings.HasSuffix(name, ".wasm"):
		asset.Kind = "worker"
	}
	if strings.HasSuffix(name, ".wasm") {
		var err error
		asset.Sections, err = wasmSections(data)
		if err != nil {
			return asset, fmt.Errorf("analyze WASM %s: %w", name, err)
		}
	}
	hash := sha256.Sum256(data)
	asset.SHA256 = hex.EncodeToString(hash[:])
	if asset.Kind == "lazy" && name != "module-"+asset.SHA256+".wasm" {
		return asset, fmt.Errorf("analysis lazy asset content digest mismatch")
	}
	counter := &analysisCounter{ctx: ctx}
	encoder, _ := gzip.NewWriterLevel(counter, gzip.BestCompression)
	if _, err := io.Copy(encoder, analysisReader{ctx, bytes.NewReader(data)}); err != nil {
		_ = encoder.Close()
		return asset, err
	}
	if err := encoder.Close(); err != nil {
		return asset, err
	}
	asset.Gzip9Bytes = counter.bytes
	return asset, nil
}

func assetViolations(asset AssetAnalysis, b AnalysisBudgets) []BudgetViolation {
	raw, compressed := int64(0), int64(0)
	if asset.Kind == "core" {
		raw, compressed = b.CoreBytes, b.CoreGzip9Bytes
	} else if asset.Kind == "lazy" {
		raw, compressed = b.LazyBytes, b.LazyGzip9Bytes
	}
	var result []BudgetViolation
	if raw > 0 && asset.Bytes > raw {
		result = append(result, BudgetViolation{asset.File, "bytes", asset.Bytes, raw})
	}
	if compressed > 0 && asset.Gzip9Bytes > compressed {
		result = append(result, BudgetViolation{asset.File, "gzip9Bytes", asset.Gzip9Bytes, compressed})
	}
	return result
}

func analyzeNative(ctx context.Context, path string) (NativeAnalysis, error) {
	var result NativeAnalysis
	info, err := os.Lstat(path)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256<<20 {
		return result, fmt.Errorf("analysis native executable must be a regular file of at most 256 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hash := sha256.New()
	result.Bytes, err = io.Copy(hash, io.LimitReader(analysisReader{ctx, file}, info.Size()+1))
	if err != nil {
		return result, err
	}
	if result.Bytes != info.Size() {
		return result, fmt.Errorf("analysis native executable changed while reading")
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	build, err := buildinfo.Read(file)
	if err != nil {
		return result, fmt.Errorf("read analysis executable toolchain: %w", err)
	}
	result.Go = build.GoVersion
	for _, setting := range build.Settings {
		switch setting.Key {
		case "GOOS":
			result.GOOS = setting.Value
		case "GOARCH":
			result.GOARCH = setting.Value
		case "CGO_ENABLED":
			result.CGO = setting.Value
		}
	}
	return result, nil
}
