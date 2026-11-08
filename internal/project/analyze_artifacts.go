package project

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// RuntimeAnalysis measures caller-owned runtime files without executing a build.
// It deliberately makes no claim about their application, source or native pair.
type RuntimeAnalysis struct {
	Version     int               `json:"version"`
	AnalyzerGo  string            `json:"analyzerGo"`
	Compression string            `json:"compression"`
	Scope       string            `json:"scope"`
	Assets      []AssetAnalysis   `json:"assets"`
	Totals      AnalysisTotals    `json:"totals"`
	Budgets     AnalysisBudgets   `json:"budgets"`
	Violations  []BudgetViolation `json:"violations"`
}

// AnalyzeRuntimeArtifacts reads one existing runtime namespace. It never builds,
// executes, removes or writes the selected files. Keep them immutable during
// measurement; hashes identify the bytes read, not deployment provenance.
func AnalyzeRuntimeArtifacts(ctx context.Context, path string, budgets AnalysisBudgets) (*RuntimeAnalysis, error) {
	if err := budgets.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("analysis runtime namespace must be a directory, not a symlink")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	assets, err := analyzeRuntime(ctx, root.FS())
	if err != nil {
		return nil, err
	}
	result := &RuntimeAnalysis{Version: 1, AnalyzerGo: runtime.Version(), Compression: "go-compress-gzip-level-9",
		Scope:  "existing runtime files only; no build, source, registration, route or native-executable association is inferred",
		Assets: assets, Budgets: budgets, Violations: []BudgetViolation{}}
	for _, asset := range assets {
		result.Totals.AllBytes += asset.Bytes
		result.Totals.AllGzip9Bytes += asset.Gzip9Bytes
		if asset.Kind == "core" || asset.Kind == "bootstrap" || asset.Kind == "shim" {
			result.Totals.EagerBytes += asset.Bytes
			result.Totals.EagerGzip9Bytes += asset.Gzip9Bytes
		}
		result.Violations = append(result.Violations, assetViolations(asset, budgets)...)
	}
	return result, ctx.Err()
}
