package project

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func reproducibleProjectFixture(t *testing.T) string {
	t.Helper()
	root := fixture(t, map[string]string{
		"app/page.go": `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Stable",Body:ui.El("h1",nil,ui.Text("Stable SSR v1"))},nil}
`,
		"public/style.css":             "h1{color:navy}",
		"public/.well-known/repro.txt": "embedded public fixture",
	})
	for _, name := range []string{"eager", "lazyone", "lazytwo"} {
		dir := filepath.Join(root, "components", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		client := fmt.Sprintf("//go:build js && wasm\npackage %s\nimport(\"encoding/json\";\"github.com/Suhaibinator/Next.go/client\")\nfunc Mount(*client.Scope,json.RawMessage)error{return nil}\n", name)
		if err := os.WriteFile(filepath.Join(dir, "client_js.go"), []byte(client), 0644); err != nil {
			t.Fatal(err)
		}
		if name != "eager" {
			if err := os.WriteFile(filepath.Join(dir, "component.json"), []byte(`{"loading":"lazy"}`), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.com/site\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(repo))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}
func reproducibleBuildAssets(t *testing.T, b *Build) map[string][]byte {
	t.Helper()
	assets := map[string][]byte{}
	base := filepath.Join(b.Dir, "server", "assets")
	if err := filepath.WalkDir(base, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, name)
		if err == nil {
			assets[filepath.ToSlash(relative)] = data
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return assets
}
func assertReproducibleBuild(t *testing.T, baseline, candidate *Build) {
	t.Helper()
	if baseline.Dir == candidate.Dir {
		t.Fatal("concurrent/sequential compilation reused a generation directory")
	}
	if baseline.ID != candidate.ID || baseline.Fingerprint != candidate.Fingerprint {
		t.Fatal("unchanged inputs changed runtime identity", baseline.ID, candidate.ID, baseline.Fingerprint, candidate.Fingerprint)
	}
	want, got := reproducibleBuildAssets(t, baseline), reproducibleBuildAssets(t, candidate)
	if len(want) != len(got) {
		t.Fatal("asset set changed")
	}
	wasmCount := 0
	for name, asset := range want {
		if !bytes.Equal(asset, got[name]) {
			t.Fatal("unchanged inputs changed published asset bytes", name)
		}
		if strings.HasSuffix(name, ".wasm") {
			wasmCount++
		}
	}
	if wasmCount != 3 {
		t.Fatal("fixture must exercise core plus both lazy bundles", wasmCount)
	}
	a, err := os.ReadFile(baseline.Binary)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(candidate.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("non-prerender native bytes changed between identical builds")
	}
}
func assertReproducibleServerAssets(t *testing.T, ctx context.Context, b *Build) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	command := exec.CommandContext(ctx, b.Binary, "-addr", address)
	command.Dir = t.TempDir()
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}()
	// Compare the embedded identity representation; gzip has separate conformance tests.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: time.Second, Transport: transport}
	base := "http://" + address
	var document []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(base + "/")
		if err == nil {
			document, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode, string(document))
			}
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal("generated server exited", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Contains(document, []byte("Stable SSR v1")) {
		t.Fatal("standalone native server lost SSR", string(document))
	}
	runtimeURL := regexp.MustCompile(`data-nextgo-runtime="([^"]+)"`).FindSubmatch(document)
	if len(runtimeURL) != 2 || html.UnescapeString(string(runtimeURL[1])) != "/_nextgo/"+b.ID+"/" {
		t.Fatal("document and embedded runtime identities differ", string(document))
	}
	for name, want := range reproducibleBuildAssets(t, b) {
		target := "/" + name
		if strings.HasPrefix(name, "public/") {
			target = "/assets/" + strings.TrimPrefix(name, "public/")
		}
		response, err := client.Get(base + target)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !bytes.Equal(got, want) {
			t.Fatal("native embed served different/missing artifact", name, response.StatusCode, err)
		}
	}
}

func TestCompileReproducibleSequentialAndConcurrentGenerations(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles multiple native/WASM generations")
	}
	root := reproducibleProjectFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	build := func() (*Build, error) {
		var log bytes.Buffer
		b, err := Compile(ctx, root, false, &log)
		if err != nil {
			return nil, fmt.Errorf("%w\n%s", err, &log)
		}
		return b, nil
	}
	first, err := build()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(first.Dir)
	second, err := build()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(second.Dir)
	assertReproducibleBuild(t, first, second)
	type result struct {
		build *Build
		err   error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { b, err := build(); results <- result{b, err} }()
	}
	concurrent := []result{<-results, <-results}
	for _, r := range concurrent {
		if r.build != nil {
			defer os.RemoveAll(r.build.Dir)
		}
	}
	seen := map[string]bool{first.Dir: true, second.Dir: true}
	for _, r := range concurrent {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if seen[r.build.Dir] {
			t.Fatal("concurrent build generation collided")
		}
		seen[r.build.Dir] = true
		assertReproducibleBuild(t, first, r.build)
	}
	info, err := buildinfo.ReadFile(first.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != "example.com/site/.nextgo/generated/server" || info.Main.Path != "example.com/site" {
		t.Fatal("native main retained staging package identity", info.Path)
	}
	sdkJSON, err := goInputJSON(ctx, root, "env", "-json", "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	var sdk struct{ GOVERSION string }
	if err := json.Unmarshal(sdkJSON, &sdk); err != nil {
		t.Fatal(err)
	}
	if info.GoVersion != sdk.GOVERSION {
		t.Fatal("native toolchain differs from application context", info.GoVersion, sdk.GOVERSION)
	}
	support, err := wasmSupport(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(support, reproducibleBuildAssets(t, first)["_nextgo/"+first.ID+"/wasm_exec.js"]) {
		t.Fatal("WASM runtime support differs from paired toolchain")
	}
	assertReproducibleServerAssets(t, ctx, first)
	// A server-only source change must still alter the public runtime identity.
	page := filepath.Join(root, "app", "page.go")
	source, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, bytes.ReplaceAll(source, []byte("Stable SSR v1"), []byte("Stable SSR v2")), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := build()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(changed.Dir)
	if changed.ID == first.ID || changed.Fingerprint == first.Fingerprint {
		t.Fatal("source change did not alter build identity")
	}
	if err := os.WriteFile(page, source, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -tags=nextgo_repro_variant"))
	flagged, err := build()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(flagged.Dir)
	if flagged.ID == first.ID {
		t.Fatal("compiler flags did not alter runtime identity")
	}
}

func TestGeneratedOverlayPrerenderProbeEmbedsOriginalSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and executes the native prerender probe")
	}
	root := reproducibleProjectFixture(t)
	config := `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
var building bool
func Configure(c *nextgo.Config){building=c.PrerenderBuildMode}
`
	page := `//go:build !js
package app
import("errors";nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){if !building{return nextgo.Page{},errors.New("runtime loader intentionally unavailable")};return nextgo.Page{Title:"Stable",Body:ui.El("h1",nil,ui.Text("Stable SSR v1"))},nil}
`
	policy := `//go:build !js
package app
import("time";nextgo "github.com/Suhaibinator/Next.go")
func Prerender()*nextgo.PrerenderPolicy{return &nextgo.PrerenderPolicy{HostIndependent:true,Revalidate:time.Hour,Expire:24*time.Hour}}
`
	for name, value := range map[string]string{"config.go": config, "page.go": page, "prerender.go": policy} {
		if err := os.WriteFile(filepath.Join(root, "app", name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	b, err := Compile(ctx, root, false, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, &log)
	}
	defer os.RemoveAll(b.Dir)
	data, err := os.ReadFile(filepath.Join(b.Dir, "server", "private", "prerender.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Code  string `json:"code"`
		Pages []struct {
			GeneratedAt time.Time `json:"generatedAt"`
			HTML        string    `json:"html"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Pages) != 1 || manifest.Pages[0].GeneratedAt.IsZero() || manifest.Pages[0].HTML != "<h1>Stable SSR v1</h1>" || manifest.Code == b.ID {
		t.Fatal("probe snapshot lost its content, original age or build binding", string(data), b.ID)
	}
	assertReproducibleServerAssets(t, ctx, b)
}

func TestGeneratedOverlayIdentityEmbedAndProvenance(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles native/WASM overlay fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to inspect VCS provenance")
	}
	root := fixture(t, map[string]string{".gitignore": ".nextgo/\n"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, args := range [][]string{{"init", "-q"}, {"add", "go.mod", ".gitignore"}, {"-c", "user.name=Repro Fixture", "-c", "user.email=repro@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + t.TempDir(), "commit", "-qm", "fixture"}} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = root
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %s %v", data, err)
		}
	}
	virtual := filepath.Join(root, ".nextgo", "entry")
	var binaries, wasm []byte
	for i, name := range []string{"build-one", "build-two"} {
		physical := filepath.Join(root, ".nextgo", name)
		if err := os.MkdirAll(filepath.Join(physical, "assets"), 0755); err != nil {
			t.Fatal(err)
		}
		source := []byte("package main\nimport(\"embed\";\"fmt\")\n//go:embed all:assets\nvar files embed.FS\nfunc main(){b,_:=files.ReadFile(\"assets/value.txt\");fmt.Print(string(b))}\n")
		main := filepath.Join(physical, "main.go")
		asset := filepath.Join(physical, "assets", "value.txt")
		if err := os.WriteFile(main, source, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(asset, []byte("overlay embedded fixture"), 0644); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(virtual, "main.go"): main, filepath.Join(virtual, "assets", "value.txt"): asset}})
		if err != nil {
			t.Fatal(err)
		}
		overlay := filepath.Join(physical, "overlay.json")
		if err := os.WriteFile(overlay, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"native", "wasm"} {
			binary := filepath.Join(physical, target)
			var environment []string
			if target == "wasm" {
				environment = []string{"GOOS=js", "GOARCH=wasm"}
			}
			var log bytes.Buffer
			if err := run(ctx, root, &log, environment, "build", "-overlay="+overlay, "-trimpath", "-ldflags=-s -w", "-o", binary, virtual); err != nil {
				t.Fatalf("%s overlay: %v %s", target, err, &log)
			}
			data, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			baseline := &binaries
			if target == "wasm" {
				baseline = &wasm
			}
			if i == 0 {
				*baseline = data
			} else if !bytes.Equal(*baseline, data) {
				t.Fatalf("%s overlay bytes depend on physical generation", target)
			}
			if bytes.Contains(data, []byte(name)) {
				t.Fatal("overlay leaked physical generation", target)
			}
			if target == "native" {
				info, err := buildinfo.ReadFile(binary)
				if err != nil {
					t.Fatal(err)
				}
				settings := map[string]string{}
				for _, v := range info.Settings {
					settings[v.Key] = v.Value
				}
				if info.Path != "example.com/site/.nextgo/entry" || info.Main.Path != "example.com/site" || settings["vcs.revision"] == "" {
					t.Fatal("overlay lost module/VCS provenance", info)
				}
				printed, err := exec.CommandContext(ctx, binary).Output()
				if err != nil || string(printed) != "overlay embedded fixture" {
					t.Fatal("overlay embed failed", string(printed), err)
				}
			}
		}
	}
}
