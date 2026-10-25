//go:build !js

package buildassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func graphEmit(name, output, content string, intermediate bool) Tool {
	return Tool{Name: name, Outputs: []string{output}, Intermediate: intermediate, Transform: func(_ context.Context, w Workspace) error {
		name := filepath.Join(w.Outputs, filepath.FromSlash(output))
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return err
		}
		return os.WriteFile(name, []byte(content), 0644)
	}}
}

func graphConsume(name, output, alias, producer, input string) Tool {
	return Tool{Name: name, Dependencies: map[string]string{alias: producer}, Outputs: []string{output}, Transform: func(_ context.Context, w Workspace) error {
		data, err := os.ReadFile(filepath.Join(w.Dependencies(), alias, input))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(w.Outputs, output), data, 0644)
	}}
}

func TestAssetGraphStableOrderAndPrivateIntermediates(t *testing.T) {
	root := t.TempDir()
	producer := graphEmit("Tokens", "palette.css", "body{color:blue}", true)
	consumer := graphConsume("CSS", "style.css", "tokens", "Tokens", "palette.css")
	independent := graphEmit("Independent", "other.css", "other", false)
	var order []string
	tools := []Tool{consumer, independent, producer}
	for i := range tools {
		name, transform := tools[i].Name, tools[i].Transform
		tools[i].Transform = func(ctx context.Context, w Workspace) error { order = append(order, name); return transform(ctx, w) }
	}
	result, err := Run(context.Background(), Pipeline{Version: 2, Tools: tools}, Options{Root: root, Stage: filepath.Join(root, "stage")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"Independent", "Tokens", "CSS"}) {
		t.Fatal("unstable dependency ordering", order)
	}
	if get(t, result.Dir, "style.css") != "body{color:blue}" || get(t, result.Dir, "other.css") != "other" {
		t.Fatal("missing final outputs")
	}
	if _, err := os.Stat(filepath.Join(result.Dir, "palette.css")); !os.IsNotExist(err) {
		t.Fatal("published intermediate", err)
	}
	public := filepath.Join(root, "public")
	put(t, public, "existing.css", "existing")
	if err := Merge(context.Background(), result.Dir, public); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(public, "palette.css")); !os.IsNotExist(err) {
		t.Fatal("merged intermediate", err)
	}
	if get(t, public, "existing.css") != "existing" {
		t.Fatal("public source was replaced")
	}
}

func TestAssetGraphDependencyCopiesAndConfigurationAreIsolated(t *testing.T) {
	root := t.TempDir()
	p := Pipeline{Version: 2, Tools: []Tool{
		graphEmit("Tokens", "palette.css", "original", true),
		graphConsume("Mutator", "ignored.css", "tokens", "Tokens", "palette.css"),
		graphConsume("Final", "style.css", "tokens", "Tokens", "palette.css"),
	}}
	p.Tools[1].Intermediate = true
	p.Tools[1].Transform = func(_ context.Context, w Workspace) error {
		if data := get(t, w.Dependencies(), "tokens/palette.css"); data != "original" {
			t.Fatal(data)
		}
		if err := os.WriteFile(filepath.Join(w.Dependencies(), "tokens", "palette.css"), []byte("changed"), 0644); err != nil {
			return err
		}
		// Changes through a caller-owned Pipeline cannot alter the validated run.
		p.Tools[0].Intermediate = false
		p.Tools[2].Dependencies["tokens"] = "Mutator"
		p.Tools[2].Outputs[0] = "leaked.css"
		return os.WriteFile(filepath.Join(w.Outputs, "ignored.css"), []byte("private"), 0644)
	}
	result, err := Run(context.Background(), p, Options{Root: root, Stage: filepath.Join(root, "stage")})
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, result.Dir, "style.css"); got != "original" {
		t.Fatal("shared writable dependency", got)
	}
	entries, err := os.ReadDir(result.Dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "style.css" {
		t.Fatal("configuration mutation changed publication", entries, err)
	}
}

func TestAssetGraphOnlyDeclaredDependenciesAreAvailable(t *testing.T) {
	root := t.TempDir()
	final := graphConsume("Final", "style.css", "left", "Left", "same.css")
	consume := final.Transform
	final.Transform = func(ctx context.Context, w Workspace) error {
		entries, err := os.ReadDir(w.Dependencies())
		if err != nil || len(entries) != 1 || entries[0].Name() != "left" {
			return fmt.Errorf("undeclared dependencies visible: %v", err)
		}
		return consume(ctx, w)
	}
	result, err := Run(context.Background(), Pipeline{Version: 2, Tools: []Tool{graphEmit("Left", "same.css", "left", true), graphEmit("Right", "same.css", "right", true), final}}, Options{Root: root, Stage: filepath.Join(root, "stage")})
	if err != nil || get(t, result.Dir, "style.css") != "left" {
		t.Fatal(err)
	}
}

func TestAssetGraphRejectsInvalidGraphBeforeAnyTool(t *testing.T) {
	base := func() Pipeline {
		return Pipeline{Version: 2, Tools: []Tool{graphEmit("A", "a.css", "a", true), graphConsume("B", "b.css", "a", "A", "a.css")}}
	}
	cases := map[string]func() Pipeline{
		"unknown":         func() Pipeline { p := base(); p.Tools[1].Dependencies["a"] = "unknown"; return p },
		"self":            func() Pipeline { p := base(); p.Tools[1].Dependencies["a"] = "B"; return p },
		"cycle":           func() Pipeline { p := base(); p.Tools[0].Dependencies = map[string]string{"b": "B"}; return p },
		"case-alias":      func() Pipeline { p := base(); p.Tools[1].Dependencies["A"] = "A"; return p },
		"no-publisher":    func() Pipeline { p := base(); p.Tools[1].Intermediate = true; return p },
		"v1-dependency":   func() Pipeline { p := base(); p.Version = 1; p.Tools[0].Intermediate = false; return p },
		"v1-intermediate": func() Pipeline { p := base(); p.Version = 1; p.Tools[1].Dependencies = nil; return p },
		"public-collision": func() Pipeline {
			p := base()
			p.Tools[0].Intermediate = false
			p.Tools[1].Outputs = []string{"a.css"}
			return p
		},
		"intermediate-local-collision": func() Pipeline { p := base(); p.Tools[0].Outputs = []string{"CSS/a.css", "css/b.css"}; return p },
		"private-intermediate":         func() Pipeline { p := base(); p.Tools[0].Outputs = []string{".env.local"}; return p },
		"alias-count": func() Pipeline {
			p := base()
			p.Tools[1].Dependencies = map[string]string{}
			for i := 0; i < 65; i++ {
				p.Tools[1].Dependencies[fmt.Sprintf("a%d", i)] = "A"
			}
			return p
		},
		"edge-count": func() Pipeline {
			p := Pipeline{Version: 2}
			for i := 0; i < 52; i++ {
				p.Tools = append(p.Tools, graphEmit(fmt.Sprintf("p%d", i), "same.css", "x", true))
			}
			for i := 0; i < 5; i++ {
				tool := graphEmit(fmt.Sprintf("c%d", i), fmt.Sprintf("out%d.css", i), "x", false)
				tool.Dependencies = map[string]string{}
				for j := 0; j < 52; j++ {
					tool.Dependencies[fmt.Sprintf("p%d", j)] = fmt.Sprintf("p%d", j)
				}
				p.Tools = append(p.Tools, tool)
			}
			return p
		},
	}
	for _, alias := range []string{"../escape", "dir/name", ".env", "secrets", "_nextgo", strings.Repeat("a", 65)} {
		alias := alias
		cases["alias-"+alias] = func() Pipeline { p := base(); p.Tools[1].Dependencies = map[string]string{alias: "A"}; return p }
	}
	for name, makePipeline := range cases {
		t.Run(name, func(t *testing.T) {
			p := makePipeline()
			calls := 0
			for i := range p.Tools {
				p.Tools[i].Transform = func(context.Context, Workspace) error { calls++; return nil }
			}
			root := t.TempDir()
			stage := filepath.Join(root, "stage")
			if _, err := Run(context.Background(), p, Options{Root: root, Stage: stage}); err == nil {
				t.Fatal("accepted invalid graph")
			}
			if calls != 0 {
				t.Fatal("invalid graph executed a tool", calls)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("validation created a stage", err)
			}
		})
	}
}

func TestAssetGraphFailureCancellationAndDeadlineKeepPrevious(t *testing.T) {
	for _, scenario := range []string{"producer-failure", "missing-producer-output", "consumer-failure", "producer-cancel", "consumer-cancel", "consumer-deadline"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			p := Pipeline{Version: 2, Tools: []Tool{graphEmit("Producer", "tokens.css", "old", true), graphConsume("Final", "style.css", "tokens", "Producer", "tokens.css")}}
			first, err := Run(context.Background(), p, Options{Root: root, Stage: filepath.Join(root, "good")})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "producer-failure":
				p.Tools[0].Transform = func(context.Context, Workspace) error { return errors.New("producer failed") }
			case "missing-producer-output":
				p.Tools[0].Transform = func(context.Context, Workspace) error { return nil }
			case "consumer-failure":
				p.Tools[1].Transform = func(context.Context, Workspace) error { return errors.New("consumer failed") }
			case "producer-cancel":
				p.Tools[0].Transform = func(ctx context.Context, _ Workspace) error { cancel(); <-ctx.Done(); return ctx.Err() }
			case "consumer-cancel":
				p.Tools[1].Transform = func(ctx context.Context, _ Workspace) error { cancel(); <-ctx.Done(); return ctx.Err() }
			case "consumer-deadline":
				p.Tools[1].TimeoutSeconds = 1
				p.Tools[1].Transform = func(ctx context.Context, _ Workspace) error { <-ctx.Done(); return ctx.Err() }
			}
			stage := filepath.Join(root, "failed")
			_, err = Run(ctx, p, Options{Root: root, Stage: stage})
			if err == nil {
				t.Fatal("failed graph published")
			}
			if strings.Contains(scenario, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if scenario == "consumer-deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("failed stage retained", err)
			}
			if got := get(t, first.Dir, "style.css"); got != "old" {
				t.Fatal("previous generation mutated", got)
			}
		})
	}
}

func TestAssetGraphSharedArtifactCopyQuota(t *testing.T) {
	for _, scenario := range []string{"bytes", "files"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "verified")
			put(t, source, "tokens.css", "abc")
			q := &quota{}
			if scenario == "bytes" {
				q.bytes = maxTotal - 5
			} else {
				q.files = maxFiles - 1
			}
			first := filepath.Join(root, "first")
			if err := collectWithQuota(context.Background(), source, first, []string{"tokens.css"}, q); err != nil {
				t.Fatal(err)
			}
			second := filepath.Join(root, "second")
			if err := collectWithQuota(context.Background(), source, second, []string{"tokens.css"}, q); err == nil {
				t.Fatal("quota reset for next graph copy")
			}
			if _, err := os.Stat(filepath.Join(second, "tokens.css")); !os.IsNotExist(err) {
				t.Fatal("copied after shared quota exhausted", err)
			}
		})
	}
}

func TestAssetGraphCommandProcess(t *testing.T) {
	if os.Getenv("TEST_NEXTGO_ASSET_GRAPH_HELPER") != "1" {
		return
	}
	w, err := WorkspaceFromEnv()
	if err != nil {
		os.Exit(1)
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "legacy" {
		if os.Args[len(os.Args)-2] != "{dependencies}" || os.Getenv("GRAPH_LITERAL") != "{dependencies}" {
			os.Exit(8)
		}
		if _, err := os.Stat(w.Dependencies()); !os.IsNotExist(err) {
			os.Exit(9)
		}
		if err := os.WriteFile(filepath.Join(w.Outputs, "style.css"), []byte("legacy"), 0644); err != nil {
			os.Exit(10)
		}
		os.Exit(0)
	}
	if w.Dependencies() != os.Getenv("NEXTGO_ASSET_DEPENDENCY_DIR") {
		os.Exit(2)
	}
	var data []byte
	if mode == "producer" {
		data, err = os.ReadFile(filepath.Join(w.Inputs, "styles", "main.css"))
	} else {
		if filepath.Clean(os.Getenv("GRAPH_DEP_ARG")) != filepath.Join(w.Dependencies(), "tokens", "palette.css") {
			os.Exit(3)
		}
		if os.Args[len(os.Args)-2] != os.Getenv("GRAPH_DEP_ARG") || os.Args[len(os.Args)-3] != "$(touch should-not-exist)" {
			os.Exit(7)
		}
		if os.Getenv("GRAPH_LITERAL") != "$(touch should-not-exist)" {
			os.Exit(4)
		}
		data, err = os.ReadFile(os.Getenv("GRAPH_DEP_ARG"))
	}
	if err != nil {
		os.Exit(5)
	}
	name := "palette.css"
	if mode == "consumer" {
		name = "style.css"
	}
	if err := os.WriteFile(filepath.Join(w.Outputs, name), data, 0644); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}

func TestAssetGraphExternalArgvWorkspaceAndVersion1Compatibility(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "body{color:green}")
	tool := func(name, mode, output string) Tool {
		return Tool{Name: name, Command: []string{os.Args[0], "-test.run=^TestAssetGraphCommandProcess$", "--", mode}, Inputs: []string{"styles"}, Outputs: []string{output}, Env: map[string]string{"TEST_NEXTGO_ASSET_GRAPH_HELPER": "1"}}
	}
	producer := tool("Producer", "producer", "palette.css")
	producer.Intermediate = true
	consumer := tool("Consumer", "consumer", "style.css")
	consumer.Command = append(consumer.Command[:len(consumer.Command)-1], "$(touch should-not-exist)", "{dependencies}/tokens/palette.css", "consumer")
	consumer.Dependencies = map[string]string{"tokens": "Producer"}
	consumer.Env["GRAPH_DEP_ARG"] = "{dependencies}/tokens/palette.css"
	consumer.Env["GRAPH_LITERAL"] = "$(touch should-not-exist)"
	result, err := Run(context.Background(), Pipeline{Version: 2, Tools: []Tool{consumer, producer}}, Options{Root: root, Stage: filepath.Join(root, "graph")})
	if err != nil || get(t, result.Dir, "style.css") != "body{color:green}" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("shell executed", err)
	}
	legacyCommand := Tool{Name: "Legacy argv", Command: []string{os.Args[0], "-test.run=^TestAssetGraphCommandProcess$", "--", "{dependencies}", "legacy"}, Outputs: []string{"style.css"}, Env: map[string]string{"TEST_NEXTGO_ASSET_GRAPH_HELPER": "1", "GRAPH_LITERAL": "{dependencies}"}}
	if _, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{legacyCommand}}, Options{Root: root, Stage: filepath.Join(root, "v1-argv")}); err != nil {
		t.Fatal("changed v1 literal argv", err)
	}
	legacy := graphEmit("Legacy", "style.css", "legacy", false)
	legacy.Transform = func(_ context.Context, w Workspace) error {
		if _, err := os.Stat(w.Dependencies()); !os.IsNotExist(err) {
			return fmt.Errorf("version 1 created dependency state: %v", err)
		}
		return os.WriteFile(filepath.Join(w.Outputs, "style.css"), []byte("legacy"), 0644)
	}
	old, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{legacy}}, Options{Root: root, Stage: filepath.Join(root, "v1")})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := Run(context.Background(), Pipeline{Version: 2, Tools: []Tool{graphEmit("Same", "style.css", "legacy", false)}}, Options{Root: root, Stage: filepath.Join(root, "v2-equivalent")})
	if err != nil || old.Digest != v2.Digest {
		t.Fatal("equivalent final bytes changed identity", err)
	}
}

func TestAssetGraphManifestAndDeclaredSourceFingerprint(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "blue")
	producer := Tool{Name: "Tokens", Command: []string{"not-executed"}, Inputs: []string{"styles"}, Outputs: []string{"palette.css"}, Intermediate: true}
	consumer := Tool{Name: "Final", Command: []string{"not-executed"}, Dependencies: map[string]string{"tokens": "Tokens"}, Outputs: []string{"style.css"}}
	p := Pipeline{Version: 2, Tools: []Tool{producer, consumer}}
	writeManifest := func() {
		t.Helper()
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		put(t, root, ManifestName, string(raw))
	}
	fingerprint := func() string {
		t.Helper()
		value, err := Fingerprint(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	writeManifest()
	if _, present, err := Load(root); !present || err != nil {
		t.Fatal(present, err)
	}
	initial := fingerprint()
	p.Tools[1].Dependencies = map[string]string{"palette": "Tokens"}
	writeManifest()
	graphChanged := fingerprint()
	if graphChanged == initial {
		t.Fatal("dependency declaration did not change fingerprint")
	}
	put(t, root, "styles/main.css", "red")
	edited := fingerprint()
	if edited == graphChanged {
		t.Fatal("producer input edit not watched")
	}
	if err := os.Remove(filepath.Join(root, "styles", "main.css")); err != nil {
		t.Fatal(err)
	}
	missing := fingerprint()
	if missing == edited {
		t.Fatal("producer input deletion not watched")
	}
	put(t, root, "styles/main.css", "red")
	if fingerprint() != edited {
		t.Fatal("producer input recreation did not recover")
	}
	put(t, root, ".nextgo/build/asset-pipeline/tool-000/outputs/palette.css", "private generated")
	if fingerprint() != edited {
		t.Fatal("generated intermediate caused watch loop")
	}
	put(t, root, ManifestName, `{"version":2,"tools":[{"name":"Tokens","command":["tool"],"inputs":[],"outputs":["tokens.css"],"intermediate":true},{"name":"Final","command":["tool"],"inputs":[],"outputs":["style.css"],"dependencies":{"tokens":"Tokens","tokens":"Tokens"}}]}`)
	if _, _, err := Load(root); err == nil {
		t.Fatal("accepted duplicate dependency alias JSON")
	}
}

func TestAssetGraphIntermediateOutputsKeepPathAndSymlinkDenial(t *testing.T) {
	for _, mode := range []string{"symlink", "undeclared-private"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "outside.css", "not a public output")
			producer := graphEmit("Producer", "palette.css", "unused", true)
			producer.Transform = func(_ context.Context, w Workspace) error {
				if mode == "symlink" {
					return os.Symlink(filepath.Join(root, "outside.css"), filepath.Join(w.Outputs, "palette.css"))
				}
				if err := os.WriteFile(filepath.Join(w.Outputs, "palette.css"), []byte("public"), 0644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(w.Outputs, ".env.local"), []byte("private"), 0644)
			}
			consumer := graphConsume("Final", "style.css", "tokens", "Producer", "palette.css")
			calls := 0
			consumer.Transform = func(context.Context, Workspace) error { calls++; return nil }
			stage := filepath.Join(root, "stage")
			if _, err := Run(context.Background(), Pipeline{Version: 2, Tools: []Tool{producer, consumer}}, Options{Root: root, Stage: stage}); err == nil {
				t.Fatal("accepted unsafe intermediate output")
			}
			if calls != 0 {
				t.Fatal("exposed unverified producer output to consumer")
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("unsafe intermediate stage retained", err)
			}
		})
	}
}
