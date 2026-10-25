//go:build !js

package buildassets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, name, value string) {
	t.Helper()
	name = filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
func get(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func fixture(mode string) Tool {
	return Tool{Name: "fixture", Command: []string{os.Args[0], "-test.run=^TestAssetToolProcess$", "--", mode}, Inputs: []string{"styles"}, Outputs: []string{"css/app.css"}, Env: map[string]string{"TEST_NEXTGO_ASSET_HELPER": "1"}}
}
func TestAssetToolProcess(t *testing.T) {
	if os.Getenv("TEST_NEXTGO_ASSET_HELPER") != "1" {
		return
	}
	w, err := WorkspaceFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "sleep" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if err := os.MkdirAll(filepath.Join(w.Outputs, "css"), 0755); err != nil {
		os.Exit(2)
	}
	if mode == "symlink" {
		if err := os.Symlink(filepath.Join(w.Inputs, "styles", "main.css"), filepath.Join(w.Outputs, "css", "app.css")); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	data, err := os.ReadFile(filepath.Join(w.Inputs, "styles", "main.css"))
	if err != nil {
		os.Exit(4)
	}
	if err := os.WriteFile(filepath.Join(w.Outputs, "css", "app.css"), append([]byte("/* generated */\n"), data...), 0644); err != nil {
		os.Exit(5)
	}
	if mode == "extra" {
		_ = os.WriteFile(filepath.Join(w.Outputs, "extra.css"), []byte("extra"), 0644)
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "fixture failed")
		os.Exit(6)
	}
	if mode == "log" {
		fmt.Fprint(os.Stdout, strings.Repeat("x", maxLog*3))
	}
	os.Exit(0)
}

func TestCommandGenerationAndMerge(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "body { color: blue; }")
	p := Pipeline{Version: 1, Tools: []Tool{fixture("copy")}}
	result, err := Run(context.Background(), p, Options{Root: root, Stage: filepath.Join(root, "stage-one")})
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, result.Dir, "css/app.css"); got != "/* generated */\nbody { color: blue; }" {
		t.Fatal(got)
	}
	public := filepath.Join(root, "public")
	put(t, public, "logo.svg", "<svg/>")
	if err := Merge(context.Background(), result.Dir, public); err != nil {
		t.Fatal(err)
	}
	if get(t, public, "logo.svg") != "<svg/>" || get(t, public, "css/app.css") == "" {
		t.Fatal("merge lost assets")
	}
	if get(t, root, "styles/main.css") != "body { color: blue; }" {
		t.Fatal("source mutated")
	}
	second, err := Run(context.Background(), p, Options{Root: root, Stage: filepath.Join(root, "stage-two")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Digest != second.Digest {
		t.Fatal("identical output has different identity")
	}
	put(t, root, "styles/main.css", "body { color: cyan; }")
	third, err := Run(context.Background(), p, Options{Root: root, Stage: filepath.Join(root, "stage-three")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Digest == third.Digest {
		t.Fatal("changed output retained immutable identity")
	}
}

func TestFailedGenerationKeepsPrevious(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "old")
	first, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{fixture("copy")}}, Options{Root: root, Stage: filepath.Join(root, "good")})
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "styles/main.css", "new")
	stage := filepath.Join(root, "failed")
	var log bytes.Buffer
	_, err = Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{fixture("fail")}}, Options{Root: root, Stage: stage, Log: &log})
	if err == nil || !strings.Contains(log.String(), "fixture failed") {
		t.Fatalf("failure missing: %v %s", err, log.String())
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("failed generation not cleaned up", err)
	}
	if get(t, first.Dir, "css/app.css") != "/* generated */\nold" {
		t.Fatal("previous generation changed")
	}
}

func TestCommandCancellationAndBoundedLogs(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "body{}")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, Pipeline{Version: 1, Tools: []Tool{fixture("sleep")}}, Options{Root: root, Stage: filepath.Join(root, "cancel")})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatal("command ignored context", err)
	}
	var log bytes.Buffer
	_, err = Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{fixture("log")}}, Options{Root: root, Stage: filepath.Join(root, "logs"), Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if log.Len() > maxLog+100 || !strings.Contains(log.String(), "log truncated") {
		t.Fatal("logs not bounded", log.Len())
	}
}

func TestTypedTransformAndLiteralArgv(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "a{}")
	hook := Tool{Name: "Go transform", Inputs: []string{"styles/main.css"}, Outputs: []string{"app.css"}, Transform: func(ctx context.Context, w Workspace) error {
		data, err := os.ReadFile(filepath.Join(w.Inputs, "styles", "main.css"))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(w.Outputs, "app.css"), bytes.ToUpper(data), 0644)
	}}
	result, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{hook}}, Options{Root: root, Stage: filepath.Join(root, "hook")})
	if err != nil {
		t.Fatal(err)
	}
	if get(t, result.Dir, "app.css") != "A{}" {
		t.Fatal("hook did not run")
	}
	// No shell is involved: even the executable name is not split or evaluated.
	tool := fixture("copy")
	tool.Command = []string{"definitely-not-a-tool; touch should-not-exist"}
	_, err = Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{tool}}, Options{Root: root, Stage: filepath.Join(root, "argv")})
	if err == nil {
		t.Fatal("invalid executable accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("shell side effect")
	}
}

func TestUnsafeDeclarationsAndOutputs(t *testing.T) {
	for _, name := range []string{"../outside.css", "/absolute.css", "css/../a.css", "css\\a.css", ".env", "config/.env.local", "private.key", ".git/config", "_nextgo/app.css", "css/.secret", "credentials.json"} {
		t.Run(name, func(t *testing.T) {
			tool := fixture("copy")
			tool.Outputs = []string{name}
			if err := validate(Pipeline{Version: 1, Tools: []Tool{tool}}); err == nil {
				t.Fatal("unsafe output accepted")
			}
		})
	}
	for _, pair := range [][2]string{{"app.css", "app.css"}, {"CSS/app.css", "css/APP.css"}, {"css", "css/app.css"}, {"CSS/a.css", "css/b.css"}} {
		tool := fixture("copy")
		tool.Outputs = []string{pair[0], pair[1]}
		if err := validate(Pipeline{Version: 1, Tools: []Tool{tool}}); err == nil {
			t.Fatal("collision accepted", pair)
		}
	}
	for _, mode := range []string{"extra", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "styles/main.css", "x")
			_, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{fixture(mode)}}, Options{Root: root, Stage: filepath.Join(root, "stage")})
			if err == nil {
				t.Fatal("unsafe output accepted")
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		put(t, root, "styles/main.css", "x")
		tool := fixture("copy")
		tool.Outputs = []string{"css/missing.css"}
		_, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{tool}}, Options{Root: root, Stage: filepath.Join(root, "stage")})
		if err == nil {
			t.Fatal("missing output accepted")
		}
	})
}

func TestPrivateAndSymlinkInputs(t *testing.T) {
	for _, kind := range []string{"secret", "link", "parent-link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "real/main.css", "x")
			switch kind {
			case "secret":
				put(t, root, "styles/.env.local", "secret")
			case "link":
				put(t, root, "styles/keep.css", "x")
				if err := os.Symlink(filepath.Join(root, "real/main.css"), filepath.Join(root, "styles/main.css")); err != nil {
					t.Skip(err)
				}
			case "parent-link":
				if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "styles")); err != nil {
					t.Skip(err)
				}
			}
			_, err := Run(context.Background(), Pipeline{Version: 1, Tools: []Tool{fixture("copy")}}, Options{Root: root, Stage: filepath.Join(root, "stage")})
			if err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}

func TestMergeRefusesCollisionBeforeWriting(t *testing.T) {
	for _, existing := range []string{"css/app.css", "CSS/APP.css", "css"} {
		t.Run(existing, func(t *testing.T) {
			root := t.TempDir()
			generated, public := filepath.Join(root, "generated"), filepath.Join(root, "public")
			put(t, generated, "a-first.css", "new")
			put(t, generated, "css/app.css", "new")
			put(t, public, existing, "old")
			if err := Merge(context.Background(), generated, public); err == nil {
				t.Fatal("collision accepted")
			}
			if get(t, public, existing) != "old" {
				t.Fatal("existing asset changed")
			}
			if _, err := os.Stat(filepath.Join(public, "a-first.css")); !os.IsNotExist(err) {
				t.Fatal("merge wrote before preflight completed")
			}
		})
	}
}

func TestMergePreservesWellKnownPublicDirectory(t *testing.T) {
	root := t.TempDir()
	generated, public := filepath.Join(root, "generated"), filepath.Join(root, "public")
	put(t, generated, "style.css", "new")
	put(t, public, ".well-known/security.txt", "existing public document")
	if err := Merge(context.Background(), generated, public); err != nil {
		t.Fatal(err)
	}
	if get(t, public, ".well-known/security.txt") != "existing public document" {
		t.Fatal("public document changed")
	}
}

func TestCancelledEmptyPipelineDoesNotPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if _, err := Run(ctx, Pipeline{Version: 1}, Options{Root: root, Stage: stage}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("cancelled pipeline created stage", err)
	}
}

func TestManifestFingerprintTracksInputsAndRecovery(t *testing.T) {
	root := t.TempDir()
	put(t, root, "styles/main.css", "blue")
	p := Pipeline{Version: 1, Tools: []Tool{fixture("copy")}}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, ManifestName, string(data))
	value := func() string {
		t.Helper()
		s, err := Fingerprint(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	previous := value()
	for _, change := range []func(){func() { put(t, root, "styles/main.css", "cyan") }, func() { put(t, root, "styles/new.scss", "new") }, func() {
		if err := os.Remove(filepath.Join(root, "styles/new.scss")); err != nil {
			t.Fatal(err)
		}
	}} {
		change()
		next := value()
		if next == previous {
			t.Fatal("input edit not watched")
		}
		previous = next
	}
	put(t, root, ".nextgo/output.css", "ignore")
	if value() != previous {
		t.Fatal("generated output caused fingerprint loop")
	}
	put(t, root, ManifestName, "{bad-one")
	one := value()
	put(t, root, ManifestName, "{bad-two")
	if one == value() {
		t.Fatal("invalid manifest edits not watched")
	}
	if _, present, err := Load(root); !present || err == nil {
		t.Fatal("invalid manifest loaded")
	}
	put(t, root, ManifestName, string(data))
	if _, present, err := Load(root); !present || err != nil {
		t.Fatal(present, err)
	}
	if err := os.Remove(filepath.Join(root, "styles/main.css")); err != nil {
		t.Fatal(err)
	}
	previous = value()
	put(t, root, "styles/main.css", "back")
	if value() == previous {
		t.Fatal("missing input recovery not watched")
	}
}

func TestManifestStrictAndOptional(t *testing.T) {
	root := t.TempDir()
	if _, present, err := Load(root); present || err != nil {
		t.Fatal(present, err)
	}
	for _, data := range []string{`{"version":2,"tools":[]}`, `{"version":1,"unknown":true,"tools":[]}`, `{"version":1,"tools":[]} {}`} {
		put(t, root, ManifestName, data)
		if _, _, err := Load(root); err == nil {
			t.Fatal("invalid manifest accepted", data)
		}
	}
}
