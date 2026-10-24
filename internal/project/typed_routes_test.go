package project

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTypedRoutesDeduplicateBranchesAndResolveEntireNamespace(t *testing.T) {
	p := &Project{Routes: []Route{
		{Pattern: "/"}, {Pattern: "/index"}, {Pattern: "/posts/:slug"},
		{Pattern: "/read-posts-by-slug"}, {Pattern: "/posts-by-slug-params"},
		{Pattern: "/blog-post"}, {Pattern: "/blog/post"}, {Pattern: "/:user_id/:userId"},
		{Pattern: "/docs/*path"}, {Pattern: "/catalog/*path?"}, {Pattern: "/api/health", API: true},
		{Pattern: "/owner", Missing: true, Slots: []Slot{{Routes: []Route{{Pattern: "/posts/:slug"}, {Pattern: "/named"}}, Intercepts: []Route{{Pattern: "/posts/:slug"}}, DefaultRoute: &Route{Pattern: "/ignored", Default: true}}}},
	}}
	source, err := TypedRoutesSource(p)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := typedRoutes(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 12 {
		t.Fatal(len(routes), string(source))
	}
	file, err := parser.ParseFile(token.NewFileSet(), "routes.go", source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if seen[fn.Name.Name] {
				t.Fatal("duplicate", fn.Name)
			}
			seen[fn.Name.Name] = true
		}
		if group, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range group.Specs {
				if value, ok := spec.(*ast.TypeSpec); ok {
					if seen[value.Name.Name] {
						t.Fatal("duplicate", value.Name)
					}
					seen[value.Name.Name] = true
				}
			}
		}
	}
	if !strings.Contains(string(source), "UserId_1 string") || !strings.Contains(string(source), "UserId_2 string") {
		t.Fatal("colliding fields not disambiguated", string(source))
	}
	if strings.Contains(string(source), "/ignored") || strings.Contains(string(source), "/owner") {
		t.Fatal("synthetic URL exported")
	}
	for i, j := 0, len(p.Routes)-1; i < j; i, j = i+1, j-1 {
		p.Routes[i], p.Routes[j] = p.Routes[j], p.Routes[i]
	}
	second, err := TypedRoutesSource(p)
	if err != nil || !bytes.Equal(source, second) {
		t.Fatal("generation depends on traversal order", err)
	}
	if len(file.Imports) != 1 || file.Imports[0].Path.Value != `"github.com/Suhaibinator/Next.go/routeinfo"` {
		t.Fatal("generated graph imports application code", file.Imports)
	}
}

func TestTypedRouteGenerationOwnsOnlyItsOutputAndChecksDrift(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "app/posts/param_slug/page.go": "package slug"})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateRoutes(p, true); err == nil {
		t.Fatal("missing output passed check")
	}
	if _, err := os.Stat(filepath.Dir(routesFile(root))); !os.IsNotExist(err) {
		t.Fatal("check created output", err)
	}
	path, err := GenerateRoutes(p, false)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if _, err := GenerateRoutes(p, true); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateRoutes(p, false); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.Stat(path)
	if !info.ModTime().Equal(unchanged.ModTime()) {
		t.Fatal("unchanged output rewritten")
	}
	custom := filepath.Join(filepath.Dir(path), "custom.go")
	if err := os.WriteFile(custom, []byte("package routes\nconst ApplicationValue=1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p.Routes = append(p.Routes, Route{Pattern: "/new"})
	prior, _ := os.ReadFile(path)
	if _, err := GenerateRoutes(p, true); err == nil {
		t.Fatal("stale output passed check")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, prior) {
		t.Fatal("check mutated stale source")
	}
	if _, err := GenerateRoutes(p, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(custom); string(got) != "package routes\nconst ApplicationValue=1\n" {
		t.Fatal("custom source changed")
	}
	if err := os.WriteFile(path, []byte("package routes\n// Owned by the application\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateRoutes(p, false); err == nil {
		t.Fatal("unowned source overwritten")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "Owned by the application") {
		t.Fatal("unowned source changed")
	}
}

func TestTypedRouteGenerationRejectsSymlinksAndPackageConflicts(t *testing.T) {
	for _, kind := range []string{"directory", "file", "package", "module"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t, map[string]string{"app/page.go": "package app"})
			p, err := Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(routesFile(root))
			outside := t.TempDir()
			if kind == "directory" {
				err = os.Symlink(outside, dir)
			} else {
				err = os.Mkdir(dir, 0755)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "file":
					err = os.Symlink(filepath.Join(outside, "victim"), routesFile(root))
				case "package":
					err = os.WriteFile(filepath.Join(dir, "custom.go"), []byte("package different"), 0644)
				case "module":
					err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/nested"), 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := GenerateRoutes(p, false); err == nil {
				t.Fatal("accepted", kind)
			}
			entries, _ := os.ReadDir(outside)
			if len(entries) != 0 {
				t.Fatal("followed symlink")
			}
		})
	}
}

func TestTypedRouteInputSnapshotIgnoresOnlyOwnGeneratedOutput(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
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
	after, err := sourceFingerprint(root)
	if err != nil || before != after {
		t.Fatal("generation changed input snapshot", err)
	}
	fullAfter, _ := Fingerprint(root)
	if fullBefore == fullAfter {
		t.Fatal("watch/build identity ignored generated source")
	}
	if err := os.WriteFile(filepath.Join(root, "app/page.go"), []byte("package app\n// changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, _ := sourceFingerprint(root)
	if reflect.DeepEqual(changed, after) {
		t.Fatal("input snapshot missed source edit")
	}
}

func TestGeneratedRoutesCompileValidateAndKeepServerCodeOutOfWASM(t *testing.T) {
	root := fixture(t, map[string]string{
		"app/page.go":                       "package app\nimport _ \"github.com/Suhaibinator/Next.go/serveronly\"\n",
		"app/posts/param_slug/page.go":      "package slug",
		"app/docs/catchall_path/page.go":    "package path",
		"app/catalog/optional_path/page.go": "package path",
	})
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/site\n\ngo 1.24\nrequire " + framework + " v0.0.0\nreplace " + framework + " => " + strconv.Quote(repo) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateRoutes(p, false); err != nil {
		t.Fatal(err)
	}
	testSource := `package routes_test
import("testing";"reflect";"example.com/site/routes")
func TestPaths(t *testing.T){
 if routes.Index()!="/"{t.Fatal("index")}
 path,err:=routes.PostsBySlug(routes.PostsBySlugParams{Slug:"Go WASM?#%"})
 if err!=nil||path!="/posts/Go%20WASM%3F%23%25"{t.Fatal(path,err)}
 for _,v:=range []string{"","..","a/b"}{if _,err:=routes.PostsBySlug(routes.PostsBySlugParams{Slug:v});err==nil{t.Fatal("invalid",v)}}
 p,err:=routes.ReadDocsByPath(map[string]string{"path":"guide/Go WASM","ancestor":"allowed"})
 if err!=nil||!reflect.DeepEqual(p.Path,[]string{"guide","Go WASM"}){t.Fatal(p,err)}
 p.Path[0]="changed"
 next,_:=routes.ReadDocsByPath(map[string]string{"path":"guide/Go WASM"});if next.Path[0]!="guide"{t.Fatal("aliased slice")}
 if _,err:=routes.ReadPostsBySlug(nil);err==nil{t.Fatal("missing single parameter")}
 if _,err:=routes.ReadDocsByPath(nil);err==nil{t.Fatal("missing catchall")}
 catalog,err:=routes.ReadCatalogByPath(nil);if err!=nil||len(catalog.Path)!=0{t.Fatal(catalog,err)}
 path,err=routes.CatalogByPath(catalog);if err!=nil||path!="/catalog"{t.Fatal(path,err)}
}
`
	file := filepath.Join(root, "routes", "runtime_test.go")
	if err := os.WriteFile(file, []byte(testSource), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(environment []string, args ...string) ([]byte, error) {
		command := exec.Command("go", args...)
		command.Dir = root
		command.Env = append(command.Environ(), environment...)
		return command.CombinedOutput()
	}
	if output, err := run([]string{"GOWORK=off"}, "test", "./routes"); err != nil {
		t.Fatalf("generated native runtime: %v\n%s", err, output)
	}
	if output, err := run([]string{"GOWORK=off", "GOOS=js", "GOARCH=wasm"}, "test", "-c", "-o", filepath.Join(root, "routes.test.wasm"), "./routes"); err != nil {
		t.Fatalf("generated WASM: %v\n%s", err, output)
	}
	deps, err := run([]string{"GOWORK=off", "GOOS=js", "GOARCH=wasm"}, "list", "-deps", "./routes")
	if err != nil {
		t.Fatal(err, string(deps))
	}
	for _, dep := range strings.Fields(string(deps)) {
		if dep == framework || dep == framework+"/serveronly" || strings.HasPrefix(dep, "example.com/site/app") {
			t.Fatal("server code in browser graph", dep)
		}
	}
	for _, bad := range []string{
		`var _,_ = routes.PostsBySlug(routes.PostsBySlugParams{Slug:[]string{"wrong"}})`,
		`var _,_ = routes.DocsByPath(routes.DocsByPathParams{Path:"wrong"})`,
		`var _,_ = routes.PostsBySlug(routes.PostsBySlugParams{Unknown:"wrong"})`,
	} {
		if err := os.WriteFile(filepath.Join(root, "routes", "bad_test.go"), []byte("package routes_test\nimport \"example.com/site/routes\"\n"+bad), 0644); err != nil {
			t.Fatal(err)
		}
		if output, err := run([]string{"GOWORK=off"}, "test", "./routes"); err == nil || !strings.Contains(string(output), "build failed") {
			t.Fatalf("bad parameter compiled or failed for another reason: %v\n%s", err, output)
		}
	}
}

func TestSourceFingerprintSupportsSymlinkedApplicationRoots(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	alias := filepath.Join(t.TempDir(), "application")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	before, err := sourceFingerprint(alias)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Discover(alias)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateRoutes(p, false); err != nil {
		t.Fatal(err)
	}
	after, err := sourceFingerprint(alias)
	if err != nil || before != after {
		t.Fatal("generation changed input snapshot through an alias", err)
	}
}
