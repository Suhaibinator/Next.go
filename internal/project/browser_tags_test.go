package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestApplicationBuildTagParsingAndReservedTarget(t *testing.T) {
	for _, test := range []struct {
		flags string
		want  []string
	}{
		{"", nil},
		{"-mod=readonly -tags=alpha,beta", []string{"alpha", "beta"}},
		{`"-tags=alpha beta"`, []string{"alpha", "beta"}},
		{`-tags='alpha'`, []string{"alpha"}},
		{`"-tags='alpha,beta' gamma"`, []string{"alpha,beta", "gamma"}},
		{"--tags=alpha,,beta -tags=gamma", []string{"gamma"}},
		{"-tags=alpha -tags=", nil},
		{`"-ldflags=-X main.value=synthetic value" -tags=alpha`, []string{"alpha"}},
	} {
		got, err := applicationBuildTags(test.flags)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("build tags %q: %q, %v; want %q", test.flags, got, err, test.want)
		}
		flag, err := buildTagsFlag(got)
		if err != nil {
			t.Fatal(err)
		}
		// This is Go's word-list grammar, not a shell-escaped command string.
		roundTrip, err := goFlagWords(strings.TrimPrefix(flag, "-tags="))
		if err != nil || len(roundTrip) != len(got) {
			t.Fatalf("tag representation changed: %q, %v", roundTrip, err)
		}
		for i := range got {
			if got[i] != roundTrip[i] {
				t.Fatal("tag representation changed", got, roundTrip)
			}
		}
	}
	for _, flags := range []string{`"unclosed`, "-tags", "-tags=nextgo_module", `"-tags=alpha nextgo_module"`, "-tags=nextgo_devrefresh", `"-tags=alpha nextgo_devrefresh"`} {
		if _, err := applicationBuildTags(flags); err == nil {
			t.Fatal("invalid or reserved tags accepted", flags)
		}
	}
}

func TestDevRefreshPrivateBuildTagsPreserveActualSelection(t *testing.T) {
	root := fixture(t, map[string]string{
		"shared/base.go":        "package shared",
		"shared/refresh.go":     "//go:build nextgo_devrefresh\n\npackage shared",
		"shared/module.go":      "//go:build nextgo_module\n\npackage shared",
		"shared/application.go": "//go:build alpha\n\npackage shared",
	})
	t.Setenv("GOFLAGS", "-tags=alpha")
	core, module, err := browserBuildTags(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	refreshCore, refreshModule, err := devRefreshBrowserBuildTags(core, module)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		flag            string
		refresh, module bool
	}{{core, false, false}, {module, false, true}, {refreshCore, true, false}, {refreshModule, true, true}} {
		command := exec.Command("go", "list", test.flag, "-f", `{{join .GoFiles ","}}`, "./shared")
		command.Dir = root
		command.Env = refreshBrowserEnvironment(command.Environ())
		data, err := command.CombinedOutput()
		if err != nil || strings.Contains(string(data), "refresh.go") != test.refresh || strings.Contains(string(data), "module.go") != test.module || !strings.Contains(string(data), "application.go") {
			t.Fatalf("actual selected private/application targets differ: %q: %s (%v)", test.flag, data, err)
		}
	}
	legacy, err := buildTagsFlag([]string{"literal,comma", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	merged, _, err := devRefreshBrowserBuildTags(legacy, module)
	words, parseErr := goFlagWords(strings.TrimPrefix(merged, "-tags="))
	if err != nil || parseErr != nil || !reflect.DeepEqual(words, []string{"literal,comma", "alpha", devRefreshBuildTag}) {
		t.Fatal("private merge changed legacy literal tag semantics", words, err, parseErr)
	}
}

func TestBrowserTagsPreserveGoSelectedFiles(t *testing.T) {
	root := fixture(t, map[string]string{
		"shared/base.go":   "package shared",
		"shared/alpha.go":  "//go:build alpha\n\npackage shared",
		"shared/beta.go":   "//go:build beta\n\npackage shared",
		"shared/gamma.go":  "//go:build gamma\n\npackage shared",
		"shared/module.go": "//go:build nextgo_module\n\npackage shared",
	})
	for _, flags := range []string{"", "-mod=readonly -tags=alpha,beta", `"-tags=alpha beta"`, `"-tags='alpha,beta' gamma"`, "-tags=alpha -tags=beta"} {
		t.Run(flags, func(t *testing.T) {
			t.Setenv("GOFLAGS", flags)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			core, module, err := browserBuildTags(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			selected := func(extra ...string) string {
				args := append([]string{"list", "-f", `{{join .GoFiles ","}}`}, extra...)
				cmd := exec.CommandContext(ctx, "go", append(args, "./shared")...)
				cmd.Dir = root
				data, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("Go selected files: %v %s", err, data)
				}
				return strings.TrimSpace(string(data))
			}
			original := selected()
			if selected(core) != original {
				t.Fatal("core tags changed compiler-selected files")
			}
			lazy := strings.Replace(selected(module), ",module.go", "", 1)
			if lazy != original {
				t.Fatal("module tags changed application file selection", lazy, original)
			}
		})
	}
}

func TestLazyTargetCompilesApplicationTagsAndChecksItsOwnGraph(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := fixture(t, map[string]string{
		"app/page.go": `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("Build target fixture")},nil}`,
		"app/shared_js.go":               "//go:build js && wasm\n\npackage app\nconst BrowserValue=1",
		"components/lazy/component.json": `{"loading":"lazy"}`,
		"components/lazy/client_js.go": `//go:build js && wasm
package lazy
import("encoding/json";"example.com/site/shared";"github.com/Suhaibinator/Next.go/client")
func Mount(*client.Scope,json.RawMessage)error{_ = shared.Value;return client.Navigate("/about")}`,
		"shared/value_js.go": "//go:build js && wasm && alpha && beta\n\npackage shared\nconst Value=1",
	})
	module := fmt.Sprintf("module example.com/site\n\ngo 1.24\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(frameworkRoot))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", `"-tags=alpha beta" -mod=readonly`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var output bytes.Buffer
	build, err := Compile(ctx, root, false, &output)
	if err != nil {
		t.Fatalf("tagged lazy build failed: %v\n%s", err, &output)
	}
	defer os.RemoveAll(build.Dir)
	files, err := filepath.Glob(filepath.Join(build.Dir, "server/assets/_nextgo/*/module-*.wasm"))
	if err != nil || len(files) != 1 {
		t.Fatal("missing lazy executable", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("Incompatible document configuration; reload the page")) {
		t.Fatal("lazy Navigate retained document-router startup")
	}
	// The server import appears only with the private module tag. Validation
	// must inspect that exact graph, rather than validating the eager target.
	bad := filepath.Join(root, "components/lazy/bad_js.go")
	if err := os.WriteFile(bad, []byte("//go:build js && wasm && nextgo_module\n\npackage lazy\nimport _ \"example.com/site/app\""), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	_, tags, err := browserBuildTags(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := filepath.Glob(filepath.Join(build.Dir, "module-*/main.go"))
	if len(entry) != 1 {
		t.Fatal("missing generated module entrypoint")
	}
	err = validateBrowserGraph(ctx, p, filepath.Dir(entry[0]), tags)
	if err == nil || !strings.Contains(err.Error(), "application route code") {
		t.Fatalf("module-only server dependency accepted: %v", err)
	}
}
