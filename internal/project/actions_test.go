package project

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func actionFixtureFiles(name string) map[string]string {
	return map[string]string{
		"app/page.go": "package app",
		"actions/" + name + "/contract.go": `package operation
import "github.com/Suhaibinator/Next.go/mutation"
type Input struct { Amount int ` + "`json:\"amount\"`" + ` }
type Output struct { Count int ` + "`json:\"count\"`" + ` }
var Contract=mutation.Must[Input,Output](` + strconv.Quote(name) + `,"v1")
`,
		"actions/" + name + "/action.go": `//go:build !js

package operation
import("net/url";"strconv";"github.com/Suhaibinator/Next.go/actions";"github.com/Suhaibinator/Next.go/mutation";_ "github.com/Suhaibinator/Next.go/serveronly")
const serverSecret="NATIVE_ACTION_SECRET_MUST_NOT_ENTER_BROWSER"
func Handle(c *actions.Context,in Input)(mutation.Result[Output],error){if in.Amount==-999{panic(serverSecret)};return mutation.Result[Output]{Data:Output{Count:in.Amount+1},Redirect:"/done",RevalidateTags:[]string{"counter"}},nil}
func Options()mutation.BindOptions[Input]{return mutation.BindOptions[Input]{Authorize:func(c *actions.Context)error{if c.Request.Header.Get("X-Deny")=="1"{return actions.ErrUnauthorized};return nil},Form:func(c *actions.Context,values url.Values)(Input,error){amount,err:=strconv.Atoi(values.Get("amount"));return Input{Amount:amount},err}}}
`,
	}
}

func TestActionDiscoveryAndGeneratedRegistration(t *testing.T) {
	root := fixture(t, actionFixtureFiles("profile/save"))
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Name != "profile/save" || p.Actions[0].Revision != "v1" || p.Actions[0].Symbol != "ProfileSave" {
		t.Fatalf("%#v", p.Actions)
	}
	source := serverSource(p, "build", false)
	if !strings.Contains(source, `"example.com/site/actionrefs"`) || !strings.Contains(source, "config.ActionRegistrars=append(config.ActionRegistrars,") {
		t.Fatal("generated entrypoint does not append action registrar")
	}
	if pos := strings.Index(source, "config.ActionRegistrars=append"); pos < 0 || pos > strings.Index(source, "nextgo.NewContext(ctx,config)") {
		t.Fatal("action registrars must be configured before app construction")
	}
	sources, err := ActionSources(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sources["nextgo_gen.go"], []byte("type ProfileSaveInput = a0.Input")) || !bytes.Contains(sources["nextgo_gen_js.go"], []byte("a0.Contract.Call(ctx, input, options...)")) || !bytes.Contains(sources["nextgo_gen_js.go"], []byte("a0.Contract.CallMultipart")) || !bytes.Contains(sources["nextgo_gen_server.go"], []byte("a0.Contract.Bind(router, a0.Handle, a0.Options())")) {
		t.Fatal("generated API does not reuse typed contract")
	}
	// Discovery is parse-only: even a package initializer that would panic is
	// inspected without being executed.
	path := filepath.Join(root, "actions/profile/save/action.go")
	file, _ := os.ReadFile(path)
	file = append(file, []byte("\nfunc init(){panic(\"discovery executed application code\")}\n")...)
	if err := os.WriteFile(path, file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err != nil {
		t.Fatal(err)
	}
}

func TestActionDiscoveryRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct{ name, file, old, new string }{
		{"name mismatch", "contract.go", `"profile/save"`, `"different"`},
		{"computed revision", "contract.go", `"v1"`, `revision`},
		{"empty revision", "contract.go", `"v1"`, `""`},
		{"wrong constructor", "contract.go", `mutation.Must`, `mutation.New`},
		{"wrong type arguments", "contract.go", `[Input,Output]`, `[Input,string]`},
		{"unshared contract", "contract.go", `package operation`, `//go:build !js` + "\n\npackage operation"},
		{"native tag missing", "action.go", `//go:build !js`, ``},
		{"native wrong tag", "action.go", `//go:build !js`, `//go:build js`},
		{"server marker missing", "action.go", `;_ "github.com/Suhaibinator/Next.go/serveronly"`, ``},
		{"handle missing", "action.go", `func Handle(`, `func Another(`},
		{"options missing", "action.go", `func Options()`, `func AnotherOptions()`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := actionFixtureFiles("profile/save")
			key := "actions/profile/save/" + tt.file
			files[key] = strings.Replace(files[key], tt.old, tt.new, 1)
			if _, err := Discover(fixture(t, files)); err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
	for _, name := range []string{"bad.name", "bad space"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Discover(fixture(t, actionFixtureFiles(name))); err == nil {
				t.Fatal("invalid directory accepted")
			}
		})
	}
	for _, missing := range []string{"contract.go", "action.go"} {
		t.Run("missing "+missing, func(t *testing.T) {
			files := actionFixtureFiles("save")
			delete(files, "actions/save/"+missing)
			if _, err := Discover(fixture(t, files)); err == nil {
				t.Fatal("unpaired action accepted")
			}
		})
	}
}

func TestActionDiscoveryRejectsCollisionsAndUnsafeFiles(t *testing.T) {
	for _, names := range [][]string{{"foo-bar", "foo_bar"}, {"foo", "foo-input"}, {"register"}} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			files := map[string]string{}
			for _, name := range names {
				for key, value := range actionFixtureFiles(name) {
					files[key] = value
				}
			}
			if _, err := Discover(fixture(t, files)); err == nil {
				t.Fatal("generated symbol collision accepted")
			}
		})
	}
	for _, kind := range []string{"root symlink", "source symlink", "directory symlink", "nested module"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t, actionFixtureFiles("save"))
			outside := t.TempDir()
			switch kind {
			case "root symlink":
				if err := os.Rename(filepath.Join(root, "actions"), filepath.Join(outside, "actions")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "actions"), filepath.Join(root, "actions")); err != nil {
					t.Skip(err)
				}
			case "source symlink":
				path := filepath.Join(root, "actions/save/contract.go")
				data, _ := os.ReadFile(path)
				_ = os.WriteFile(filepath.Join(outside, "contract.go"), data, 0644)
				_ = os.Remove(path)
				if err := os.Symlink(filepath.Join(outside, "contract.go"), path); err != nil {
					t.Skip(err)
				}
			case "directory symlink":
				if err := os.Symlink(outside, filepath.Join(root, "actions/linked")); err != nil {
					t.Skip(err)
				}
			case "nested module":
				_ = os.WriteFile(filepath.Join(root, "actions/save/go.mod"), []byte("module hidden\n"), 0644)
			}
			if _, err := Discover(root); err == nil {
				t.Fatal("unsafe action source accepted")
			}
		})
	}
}

func TestActionGenerationCheckOwnershipAndStaleSet(t *testing.T) {
	root := fixture(t, actionFixtureFiles("save"))
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateActions(p, true); err == nil {
		t.Fatal("check accepted missing outputs")
	}
	if _, err := os.Stat(filepath.Join(root, "actionrefs")); !os.IsNotExist(err) {
		t.Fatal("check mode wrote directory")
	}
	paths, err := GenerateActions(p, false)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path], _ = os.ReadFile(path)
	}
	if _, err := GenerateActions(p, true); err != nil {
		t.Fatal(err)
	}
	p.Actions[0].Revision = "v2"
	if _, err := GenerateActions(p, true); err == nil {
		t.Fatal("check accepted stale outputs")
	}
	for path, data := range before {
		actual, _ := os.ReadFile(path)
		if !bytes.Equal(data, actual) {
			t.Fatal("check mode modified output")
		}
	}
	// Preflight all output ownership before publishing even the first file.
	if err := os.WriteFile(paths[1], []byte("package actionrefs\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateActions(p, false); err == nil {
		t.Fatal("unowned output overwritten")
	}
	actual, _ := os.ReadFile(paths[0])
	if !bytes.Equal(before[paths[0]], actual) {
		t.Fatal("ownership failure partially published generation")
	}
	_ = os.WriteFile(paths[1], before[paths[1]], 0644)
	p.Actions = nil
	if _, err := GenerateActions(p, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte("SaveInput")) {
			t.Fatal("removed action retained stale references")
		}
	}
	if _, err := GenerateActions(p, true); err != nil {
		t.Fatal(err)
	}
}

func TestActionGenerationRejectsOutputHazards(t *testing.T) {
	for _, kind := range []string{"directory symlink", "output symlink", "nested module", "package collision", "symbol collision", "stale output"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t, actionFixtureFiles("save"))
			p, err := Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "actionrefs")
			outside := t.TempDir()
			_ = os.MkdirAll(dir, 0755)
			switch kind {
			case "directory symlink":
				_ = os.Remove(dir)
				if err := os.Symlink(outside, dir); err != nil {
					t.Skip(err)
				}
			case "output symlink":
				_ = os.WriteFile(filepath.Join(outside, "victim.go"), []byte("package victim\n"), 0644)
				if err := os.Symlink(filepath.Join(outside, "victim.go"), actionFiles(root)[0]); err != nil {
					t.Skip(err)
				}
			case "nested module":
				_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module other\n"), 0644)
			case "package collision":
				_ = os.WriteFile(filepath.Join(dir, "custom.go"), []byte("package other\n"), 0644)
			case "symbol collision":
				_ = os.WriteFile(filepath.Join(dir, "custom.go"), []byte("package actionrefs\nvar SavePath=1\n"), 0644)
			case "stale output":
				_ = os.WriteFile(filepath.Join(dir, "nextgo_old.go"), []byte(generatedActionsHeader+"package actionrefs\n"), 0644)
			}
			if _, err := GenerateActions(p, false); err == nil {
				t.Fatal("hazard accepted")
			}
		})
	}
}

func TestActionInputSnapshotSkipsExactlyFourOwnedFiles(t *testing.T) {
	root := fixture(t, actionFixtureFiles("save"))
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := sourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	fullBefore, _ := Fingerprint(root)
	if _, err := GenerateRoutes(p, false); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateActions(p, false); err != nil {
		t.Fatal(err)
	}
	after, err := sourceFingerprint(root)
	if err != nil || before != after {
		t.Fatal("owned generation changed input-only snapshot", err)
	}
	fullAfter, _ := Fingerprint(root)
	if fullBefore == fullAfter {
		t.Fatal("full build fingerprint omitted generated files")
	}
	path := filepath.Join(root, "actionrefs/custom.go")
	_ = os.WriteFile(path, []byte("package actionrefs\n"), 0644)
	changed, _ := sourceFingerprint(root)
	if changed == after {
		t.Fatal("custom actionrefs source incorrectly skipped")
	}
}

func TestGeneratedActionsCompileNativeAndWASMWithoutServerLeaks(t *testing.T) {
	root := fixture(t, actionFixtureFiles("profile/save"))
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module example.com/site\ngo 1.24\nrequire " + framework + " v0.0.0\nreplace " + framework + " => " + strconv.Quote(repo) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateActions(p, false); err != nil {
		t.Fatal(err)
	}
	write := func(name, source string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("native/main.go", `package main
import("strings";"net/http/httptest";"example.com/site/actionrefs";"github.com/Suhaibinator/Next.go/actions")
func main(){invalidations:=0;r:=actions.New(actions.Options{OnRevalidate:func(tags,paths []string){if len(tags)!=1||tags[0]!="counter"{panic("cache effects changed")};invalidations++}});actionrefs.Register(r);q:=httptest.NewRequest("POST","https://site.test/profile/save/v1",strings.NewReader(`+"`{\"amount\":2}`"+`));q.Header.Set("Origin","https://site.test");q.Header.Set("Content-Type","application/json");w:=httptest.NewRecorder();r.ServeHTTP(w,q);if w.Code!=200||!strings.Contains(w.Body.String(),`+"`\"count\":3`"+`){panic(w.Body.String())};q=httptest.NewRequest("POST","https://site.test/profile/save/v1",strings.NewReader("amount=2"));q.Header.Set("Origin","https://site.test");q.Header.Set("Content-Type","application/x-www-form-urlencoded");w=httptest.NewRecorder();r.ServeHTTP(w,q);if w.Code!=303||w.Header().Get("Location")!="/done"||invalidations!=2{panic("progressive form/cache effects failed")};q=httptest.NewRequest("POST","https://site.test/profile/save/v1",strings.NewReader("malformed"));q.Header.Set("Origin","https://site.test");q.Header.Set("Content-Type","application/json");q.Header.Set("X-Deny","1");w=httptest.NewRecorder();r.ServeHTTP(w,q);if w.Code!=401||invalidations!=2{panic("authorization must precede decode and mutation")}}
`)
	write("browser/main_js.go", `//go:build js && wasm
package main
import("context";"example.com/site/actionrefs";"github.com/Suhaibinator/Next.go/client")
func main(){_,_=actionrefs.ProfileSave(context.Background(),actionrefs.ProfileSaveInput{Amount:2});var form *client.Multipart;_,_=actionrefs.ProfileSaveMultipart(context.Background(),form);println(actionrefs.ProfileSavePath())}
`)
	run := func(env []string, args ...string) ([]byte, error) {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Env = append(cmd.Environ(), append([]string{"GOWORK=off"}, env...)...)
		return cmd.CombinedOutput()
	}
	if output, err := run(nil, "run", "./native"); err != nil {
		t.Fatalf("native: %v\n%s", err, output)
	}
	wasm := filepath.Join(root, "browser.wasm")
	if output, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "build", "-o", wasm, "./browser"); err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	if err := validateBrowserGraph(context.Background(), p, filepath.Join(root, "browser")); err != nil {
		t.Fatal(err)
	}
	deps, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "list", "-deps", "./browser")
	if err != nil {
		t.Fatal(err, string(deps))
	}
	for _, dep := range strings.Fields(string(deps)) {
		if dep == framework+"/actions" || dep == framework+"/serveronly" || dep == "net/http" {
			t.Fatal("native dependency leaked into browser", dep)
		}
	}
	data, err := os.ReadFile(wasm)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("NATIVE_ACTION_SECRET_MUST_NOT_ENTER_BROWSER")) {
		t.Fatal("native secret leaked")
	}
	// The same shared references catch mismatched handler and caller schemas
	// with the Go compiler, rather than accepting reflection conversions.
	path := filepath.Join(root, "actions/profile/save/action.go")
	original, _ := os.ReadFile(path)
	bad := strings.Replace(string(original), "in Input)", "in *Input)", 1)
	_ = os.WriteFile(path, []byte(bad), 0644)
	if output, err := run(nil, "build", "-o", filepath.Join(root, "native-bin"), "./native"); err == nil || !strings.Contains(string(output), "cannot use") {
		t.Fatalf("mismatched handler accepted: %v\n%s", err, output)
	}
	_ = os.WriteFile(path, original, 0644)
	// Discovery declarations are verified again at registration: package init
	// reassignment cannot silently move a generated endpoint or revision.
	changed := append(append([]byte(nil), original...), []byte("\nfunc init(){Contract=mutation.Must[Input,Output](\"profile/save\",\"v2\")}\n")...)
	_ = os.WriteFile(path, changed, 0644)
	if output, err := run(nil, "run", "./native"); err == nil || !strings.Contains(string(output), "generated action contract changed") {
		t.Fatalf("runtime contract mismatch accepted: %v\n%s", err, output)
	}
	_ = os.WriteFile(path, original, 0644)
	write("browser/main_js.go", `//go:build js && wasm
package main
import("context";"example.com/site/actionrefs")
func main(){actionrefs.ProfileSave(context.Background(),"wrong")}
`)
	if output, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "build", "-o", wasm, "./browser"); err == nil || !strings.Contains(string(output), "cannot use") {
		t.Fatalf("mismatched caller accepted: %v\n%s", err, output)
	}
	// Restore a valid caller, then publish a mixed generation intentionally.
	write("browser/main_js.go", `//go:build js && wasm
package main
import("context";"example.com/site/actionrefs")
func main(){actionrefs.ProfileSave(context.Background(),actionrefs.ProfileSaveInput{})}
`)
	oldShared, _ := os.ReadFile(actionFiles(root)[0])
	p.Actions[0].Revision = "v2"
	if _, err := GenerateActions(p, false); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(actionFiles(root)[0], oldShared, 0644)
	if output, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "build", "-o", wasm, "./browser"); err == nil || !strings.Contains(string(output), "actionGeneration_") {
		t.Fatalf("mixed generation did not fail closed: %v\n%s", err, output)
	}
	if output, err := run(nil, "build", "-o", filepath.Join(root, "native-bin"), "./native"); err == nil || !strings.Contains(string(output), "actionGeneration_") {
		t.Fatalf("mixed native generation did not fail closed: %v\n%s", err, output)
	}
}
