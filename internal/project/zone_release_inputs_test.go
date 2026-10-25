package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestZoneReleaseSelectedGraphsExcludeWorkspaceAndFrameworkPeerTrees(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(strconv.FormatBool(workspace), func(t *testing.T) {
			fixture := newRefreshCompilerFixture(t)
			root := fixture.project.Root
			frameworkRoot := t.TempDir()
			peer := filepath.Join(frameworkRoot, "examples/peer")
			write := func(name, data string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(frameworkRoot, "go.mod"), "module example.com/framework\ngo 1.24\n")
			write(filepath.Join(frameworkRoot, "value/value.go"), "package value\nconst Value=1\n")
			write(filepath.Join(frameworkRoot, "value/embed.go"), "package value\nimport _ \"embed\"\n//go:embed selected.txt\nvar Text string\n")
			write(filepath.Join(frameworkRoot, "value/selected.txt"), "selected embed v1")
			write(filepath.Join(frameworkRoot, "lazyonly/value.go"), "package lazyonly\nconst Value=1\n")
			write(filepath.Join(peer, "go.mod"), "module example.com/unrelated-peer\ngo 1.24\n")
			for name, data := range map[string]string{"app/page.go": "this unrelated peer cannot compile", "public/style.css": "peer CSS", "embedded.txt": "peer embedded bytes"} {
				write(filepath.Join(peer, name), data)
			}
			mod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
			write(filepath.Join(root, "go.mod"), string(mod)+"\nrequire example.com/framework v0.0.0\nreplace example.com/framework => "+strconv.Quote(frameworkRoot)+"\n")
			if workspace {
				work := filepath.Join(t.TempDir(), "go.work")
				write(work, fmt.Sprintf("go 1.24\nuse (\n%s\n%s\n)\n", strconv.Quote(root), strconv.Quote(peer)))
				t.Setenv("GOWORK", work)
			} else {
				t.Setenv("GOWORK", "off")
			}
			fixture.native = "package main\nimport(c \"example.com/site/components/counter\";f \"example.com/framework/value\")\nfunc main(){_=c.Native();_=f.Value;_=f.Text}\n"
			fixture.core = "package main\nimport(e \"example.com/site/eager\";f \"example.com/framework/value\")\nfunc main(){_=e.Value;_=f.Value}\n"
			fixture.module = "package main\nimport(c \"example.com/site/components/counter\";b \"example.com/framework/lazyonly\")\nfunc main(){_=c.Client();_=b.Value}\n"
			plan := &zoneReleasePlan{peerRoots: []string{peer}}
			initial, err := plan.initialFingerprint(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			coreTags, moduleTags, err := browserBuildTags(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, ".nextgo", "release-closure-probes")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			var queries []refreshGraphQuery
			for _, spec := range []struct {
				name, source, tags string
				browser            bool
			}{{"native", fixture.native, "", false}, {"core", fixture.core, coreTags, true}, {"module:counter", fixture.module, moduleTags, true}} {
				query, err := refreshProbe(context.Background(), fixture.project, dir, strings.ReplaceAll(spec.name, ":", "-"), spec.source, spec.tags, spec.browser, false)
				if err != nil {
					t.Fatal(err)
				}
				query.name = spec.name
				queries = append(queries, query)
			}
			before, err := plan.collectInputs(context.Background(), fixture.project, queries, coreTags, moduleTags, fixture.native, fixture.core)
			if err != nil {
				t.Fatal(err)
			}
			// Mutate peer Go/public/embed inputs after selection, as if during build.
			for _, name := range []string{"app/page.go", "public/style.css", "embedded.txt"} {
				write(filepath.Join(peer, name), "peer changed during selected build "+name)
			}
			final, err := plan.finalFingerprint(context.Background(), fixture.project, coreTags, moduleTags)
			if err != nil || final != before {
				t.Fatalf("unrelated peer contaminated actual selected graph: workspace=%v %v", workspace, err)
			}
			afterInitial, err := plan.initialFingerprint(context.Background(), root)
			if err != nil || afterInitial != initial {
				t.Fatal("peer contaminated initial/final discovery proof", err)
			}
			// All selected native/core/lazy-only code, embeds and public bytes remain bound.
			for _, name := range []string{filepath.Join(frameworkRoot, "value/value.go"), filepath.Join(frameworkRoot, "value/selected.txt"), filepath.Join(frameworkRoot, "lazyonly/value.go"), filepath.Join(root, "public/style.css")} {
				original, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				write(name, string(original)+"\n")
				changed, err := plan.finalFingerprint(context.Background(), fixture.project, coreTags, moduleTags)
				if err != nil || changed == before {
					t.Fatal("selected dependency/embed/public edit escaped proof", name, err)
				}
				write(name, string(original))
			}
		})
	}
}

func TestZoneReleaseLocalInputsExcludeDeclaredSymlinkPeerRoot(t *testing.T) {
	root := t.TempDir()
	peer := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package app"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := &zoneReleasePlan{peerRoots: []string{canonicalInput(peer)}}
	before, err := plan.localInputs(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(peer, filepath.Join(root, "peer-link")); err != nil {
		t.Fatal(err)
	}
	after, err := plan.localInputs(context.Background(), root, false)
	if err != nil || after != before {
		t.Fatal("declared symlink peer was consumed as selected application input", err)
	}
}
