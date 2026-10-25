package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

// These inputs belong only to the independent release compiler. Ordinary build
// and dev watchers retain their broader module/workspace recovery contracts.
type zoneReleaseInputs struct {
	queries                       []refreshGraphQuery
	context, graph, local, policy string
	fingerprint                   string
	nativeSource, coreSource      string
}

func (plan *zoneReleasePlan) localInputs(ctx context.Context, root string, sourceOnly bool) (string, error) {
	root = canonicalInput(root)
	inputs := inputPlan{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, peer := range plan.peerRoots {
			if name == peer || entry.Type()&os.ModeSymlink != 0 && canonicalInput(name) == peer {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.IsDir() {
			if name != root && ignored(entry.Name()) && !pathWithin(name, filepath.Join(root, "public")) {
				return filepath.SkipDir
			}
			return nil
		}
		if environmentInput(name) {
			return nil
		}
		if len(inputs.Files) >= maxInputFiles {
			return fmt.Errorf("zone release app inputs exceed file limit")
		}
		inputs.Files = append(inputs.Files, name)
		return nil
	})
	if err != nil {
		return "", err
	}
	skip := ""
	if sourceOnly {
		skip = filepath.Join(root, "routes", "nextgo_gen.go")
	}
	local, err := hashInputsContext(ctx, inputs, skip)
	if err != nil {
		return "", err
	}
	// The pipeline's declared input semantics include non-Go files and trees.
	// It remains an explicit selected-app input, never a module-root walk.
	var pipeline strings.Builder
	if err := hashAssetPipelineInputs(ctx, root, &pipeline); err != nil {
		return "", err
	}
	return refreshDigest([]string{local, pipeline.String()}), nil
}

func releaseInputPolicy(ctx context.Context, root, coreTags, moduleTags string) (string, string, error) {
	contextDigest, files, err := refreshBuildContext(ctx, root, coreTags, moduleTags)
	if err != nil {
		return "", "", err
	}
	data, err := goInputJSON(ctx, root, "env", "-json", "GOMOD", "GOWORK", "GOFLAGS")
	if err != nil {
		return "", "", err
	}
	var environment map[string]string
	if err := json.Unmarshal(data, &environment); err != nil {
		return "", "", err
	}
	module, err := applicationModule(ctx, root)
	if err != nil {
		return "", "", err
	}
	for _, mod := range []string{environment["GOMOD"], module.GoMod} {
		if mod != "" && mod != os.DevNull {
			addUnique(&files, mod)
			addUnique(&files, strings.TrimSuffix(mod, ".mod")+".sum")
		}
	}
	if work := environment["GOWORK"]; work != "" && work != "off" {
		addUnique(&files, work)
		addUnique(&files, work+".sum")
	}
	words, err := goFlagWords(environment["GOFLAGS"])
	if err != nil {
		return "", "", err
	}
	for _, word := range words {
		if mod, ok := strings.CutPrefix(strings.TrimLeft(word, "-"), "modfile="); ok {
			mod = localInput(root, mod)
			addUnique(&files, mod)
			addUnique(&files, strings.TrimSuffix(mod, ".mod")+".sum")
		}
	}
	policy, err := hashInputsContext(ctx, inputPlan{Files: files}, "")
	return contextDigest, policy, err
}

func (plan *zoneReleasePlan) initialFingerprint(ctx context.Context, root string) (string, error) {
	local, err := plan.localInputs(ctx, root, true)
	if err != nil {
		return "", err
	}
	core, module, err := browserBuildTags(ctx, root)
	if err != nil {
		return "", err
	}
	contextDigest, policy, err := releaseInputPolicy(ctx, root, core, module)
	return refreshDigest([]string{local, contextDigest, policy}), err
}

func (plan *zoneReleasePlan) prepareInputs(ctx context.Context, p *Project, buildDir, coreTags, moduleTags string) (string, error) {
	zero := strings.Repeat("0", 64)
	var modules []lazyOutput
	var queries []refreshGraphQuery
	plan.inputTemplates = map[string]string{}
	for index, component := range p.Components {
		if !component.Lazy {
			continue
		}
		modules = append(modules, lazyOutput{Name: component.Name, File: "module-" + zero + ".wasm", Digest: zero, Epoch: zero, Size: 1})
		moduleSource := lazyMainSource(component, zero, false)
		plan.inputTemplates[refreshComponentGraphName(component.Name)] = moduleSource
		query, err := refreshProbe(ctx, p, buildDir, fmt.Sprintf("release-module-%d", index), moduleSource, moduleTags, true, false)
		if err != nil {
			return "", err
		}
		query.name = refreshComponentGraphName(component.Name)
		queries = append(queries, query)
	}
	nativeSource := zoneReleaseNativeProofSource(p)
	coreSource := clientSource(p, modules)
	plan.inputTemplates["native"], plan.inputTemplates["core"] = nativeSource, coreSource
	native, err := refreshProbe(ctx, p, buildDir, "release-native", nativeSource, "", false, true)
	if err != nil {
		return "", err
	}
	native.name = "native"
	core, err := refreshProbe(ctx, p, buildDir, "release-core", coreSource, coreTags, true, false)
	if err != nil {
		return "", err
	}
	core.name = "core"
	queries = append([]refreshGraphQuery{native, core}, queries...)
	return plan.collectInputs(ctx, p, queries, coreTags, moduleTags, nativeSource, coreSource)
}

func zoneReleaseNativeProofSource(p *Project) string {
	// Use the actual release entry and its imports, normalizing only deployment
	// literals. Release IDs, public bindings and tenants are separate from the
	// application's browser runtime identity and cannot churn it between A1/A2.
	zero := strings.Repeat("0", 64)
	return zoneReleaseServerSource(p, &zoneReleasePlan{
		selected: ZoneManifest{ID: "proof", Host: "proof.invalid"},
		spec: zonecontract.ReleaseSpec{Version: zonecontract.Version,
			Zone: zonecontract.Binding{ID: "proof", Host: "proof.invalid"}, ReleaseID: "proof",
			RuntimeID: strings.Repeat("0", 20), PublicOrigin: "https://proof.invalid",
			AssetPrefix: "/_nextgo/releases/proof", AssetsSHA256: zero, TopologySHA256: zero,
			NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol},
	})
}

func (plan *zoneReleasePlan) collectInputs(ctx context.Context, p *Project, queries []refreshGraphQuery, coreTags, moduleTags, nativeSource, coreSource string) (string, error) {
	graphs, graph, err := refreshGraphs(ctx, p, queries)
	if err != nil {
		return "", err
	}
	contextDigest, policy, err := releaseInputPolicy(ctx, p.Root, coreTags, moduleTags)
	if err != nil {
		return "", err
	}
	local, err := plan.localInputs(ctx, p.Root, false)
	if err != nil {
		return "", err
	}
	// Actual selected module metadata is policy; unused workspace/replacement
	// module trees and peer sources are deliberately absent.
	var metadata []string
	for _, g := range graphs {
		for _, pkg := range g.packages {
			if pkg.Module != nil {
				for _, mod := range []string{pkg.Module.GoMod, func() string {
					if pkg.Module.Replace != nil {
						return pkg.Module.Replace.GoMod
					}
					return ""
				}()} {
					if mod != "" {
						addUnique(&metadata, mod)
						addUnique(&metadata, strings.TrimSuffix(mod, ".mod")+".sum")
					}
				}
			}
		}
	}
	metadataDigest, err := hashInputsContext(ctx, inputPlan{Files: metadata}, "")
	if err != nil {
		return "", err
	}
	proof := &zoneReleaseInputs{queries: queries, context: contextDigest, graph: graph, local: local, policy: refreshDigest([]string{policy, metadataDigest}), nativeSource: nativeSource, coreSource: coreSource}
	proof.fingerprint = refreshDigest([]any{"nextgo.zone.release.inputs.v1", contextDigest, graph, local, proof.policy, nativeSource, coreSource, plan.inputTemplates})
	plan.inputs = proof
	return proof.fingerprint, nil
}

func (plan *zoneReleasePlan) finalFingerprint(ctx context.Context, p *Project, coreTags, moduleTags string) (string, error) {
	previous := plan.inputs
	if previous == nil {
		return "", fmt.Errorf("zone release compiler input proof missing")
	}
	// Reuse the exact stable source templates rather than dynamic content URLs.
	current, err := plan.collectInputs(ctx, p, previous.queries, coreTags, moduleTags, previous.nativeSource, previous.coreSource)
	if err != nil {
		return "", err
	}
	return current, nil
}
