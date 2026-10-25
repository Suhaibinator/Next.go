package project

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Suhaibinator/Next.go/buildassets"
)

const assetPipelineManifest = buildassets.ManifestName

func hashAssetPipelineInputs(ctx context.Context, root string, hash io.Writer) error {
	fingerprint, err := buildassets.Fingerprint(ctx, root)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(hash, "asset-pipeline:%d:%s", len(fingerprint), fingerprint)
	return err
}

// Asset tools run inside the disposable generation. A failure never mutates a
// previously published generation or the application's source/public tree.
func stageAssetPipeline(ctx context.Context, root, buildDir string, output io.Writer) (*buildassets.Result, error) {
	pipeline, present, err := buildassets.Load(root)
	if err != nil {
		return nil, fmt.Errorf("asset pipeline: %w", err)
	}
	if !present {
		return nil, nil
	}
	return buildassets.Run(ctx, pipeline, buildassets.Options{Root: root, Stage: filepath.Join(buildDir, "asset-pipeline"), Log: output})
}

func mergeGeneratedAssets(ctx context.Context, result *buildassets.Result, public string) error {
	if result == nil {
		return nil
	}
	return buildassets.Merge(ctx, result.Dir, public)
}

func generatedAssetIdentity(result *buildassets.Result) string {
	if result == nil {
		return ""
	}
	return "\x00assets:" + result.Digest
}
