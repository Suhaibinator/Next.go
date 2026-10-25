//go:build !js

package offline

import (
	"errors"
	"strings"
	"testing"
)

func sampleBuild() Build {
	spec := sampleSpec(1000)
	prefix := spec.Config.Origin + spec.Config.BasePath + "/"
	files := make([]BuildFile, len(spec.Assets))
	for i, asset := range spec.Assets {
		files[i] = BuildFile{Path: strings.TrimPrefix(asset.URL, prefix), Kind: asset.Kind, SHA256: asset.SHA256, DecodedBytes: asset.DecodedBytes, ContentType: asset.ContentType}
	}
	return Build{Version: Version, RuntimeID: spec.RuntimeID, Files: files, BridgeTemplate: "self.nextgoOffline=" + BuildDescriptorPlaceholder + ";"}
}

func TestBuildNormalizationOwnsAndBindsRelativeInventory(t *testing.T) {
	original := sampleBuild()
	build, err := NormalizeBuild(original)
	if err != nil {
		t.Fatal(err)
	}
	original.Files[0].Path = "private/secrets"
	copy := build.Clone()
	copy.Files[0].SHA256 = "mutated"
	if build.Files[0].Path == "private/secrets" || build.Files[0].SHA256 == "mutated" {
		t.Fatal("descriptor alias retained")
	}
	policy, err := Normalize(sampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	assets, err := build.Assets(policy)
	if err != nil {
		t.Fatal(err)
	}
	for i, asset := range assets {
		if asset.URL != policy.config.Origin+policy.config.BasePath+"/"+build.Files[i].Path {
			t.Fatal("untrusted origin/prefix")
		}
	}
	assets[0].SHA256 = "changed"
	if build.Files[0].SHA256 == "changed" {
		t.Fatal("asset alias retained")
	}
}

func TestBuildRejectsUnboundedForeignAndIncompleteDescriptors(t *testing.T) {
	cases := map[string]func(*Build){
		"version":  func(b *Build) { b.Version++ },
		"runtime":  func(b *Build) { b.RuntimeID = "unbound" },
		"absolute": func(b *Build) { b.Files[0].Path = "/" + b.Files[0].Path },
		"foreign-runtime": func(b *Build) {
			b.Files[0].Path = strings.Replace(b.Files[0].Path, b.RuntimeID, strings.Repeat("e", 20), 1)
		},
		"mutable-public":   func(b *Build) { b.Files[4].Path = "public/style.css" },
		"encoded":          func(b *Build) { b.Files[0].Path = strings.Replace(b.Files[0].Path, "app.wasm", "%61pp.wasm", 1) },
		"query":            func(b *Build) { b.Files[0].Path += "?v=1" },
		"duplicate":        func(b *Build) { b.Files = append(b.Files, b.Files[0]) },
		"missing-role":     func(b *Build) { b.Files = b.Files[1:] },
		"wrong-worker-sha": func(b *Build) { b.Files[1].SHA256 = strings.Repeat("d", 64) },
		"wrong-mime":       func(b *Build) { b.Files[0].ContentType = "text/html" },
		"size":             func(b *Build) { b.Files[0].DecodedBytes = 33 << 20 },
		"env": func(b *Build) {
			b.Files[4] = BuildFile{Path: "_nextgo/" + b.RuntimeID + "/offline-public/.env/icon.png", Kind: PublicAsset, SHA256: strings.Repeat("b", 64), DecodedBytes: 1, ContentType: "image/png"}
		},
		"no-template":           func(b *Build) { b.BridgeTemplate = "" },
		"no-placeholder":        func(b *Build) { b.BridgeTemplate = "self.stub=true;" },
		"duplicate-placeholder": func(b *Build) { b.BridgeTemplate += BuildDescriptorPlaceholder },
		"template-cap":          func(b *Build) { b.BridgeTemplate = strings.Repeat("x", MaxBridgeBytes) + BuildDescriptorPlaceholder },
		"template-nul":          func(b *Build) { b.BridgeTemplate += "\x00" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			b := sampleBuild()
			change(&b)
			normalized, err := NormalizeBuild(b)
			if err == nil || len(normalized.Files) != 0 {
				t.Fatal("invalid descriptor published", err)
			}
		})
	}
	if _, err := NormalizeBuild(Build{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero build accepted", err)
	}
}

func TestBuildHonorsEffectivePolicyWithoutRaisingQuotas(t *testing.T) {
	build, err := NormalizeBuild(sampleBuild())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build.Assets(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := build.Assets(new(Policy)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	c := sampleConfig()
	c.Budget.AssetBytes = 8 << 20
	p, err := Normalize(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build.Assets(p); !errors.Is(err, ErrInvalid) {
		t.Fatal("asset cap enlarged", err)
	}
	c = sampleConfig()
	c.Budget.GenerationEntries = 1
	p, err = Normalize(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build.Assets(p); !errors.Is(err, ErrQuota) {
		t.Fatal("entry budget enlarged", err)
	}
	b := sampleBuild()
	for i := range b.Files {
		b.Files[i].DecodedBytes = 32 << 20
	}
	if _, err := NormalizeBuild(b); !errors.Is(err, ErrQuota) {
		t.Fatal("total asset cap ignored", err)
	}
}
