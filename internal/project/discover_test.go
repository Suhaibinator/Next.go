package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.com/site\n\ngo 1.24\n"
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestDiscoveryAndGeneratedEntrypoint(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "app/config.go": "package app", "app/layout.go": "package app", "app/notfound.go": "package app", "app/posts/layout.go": "package posts", "app/posts/param_slug/page.go": "package slug", "app/api/health/route.go": "package health", "components/counter/client_js.go": "package counter", "app/.ignored/page.go": "this is not go"})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 3 || len(p.Components) != 1 || p.NotFound != "example.com/site/app" || p.Configure != "example.com/site/app" {
		t.Fatalf("%+v", p)
	}
	var dynamic Route
	for _, r := range p.Routes {
		if r.Pattern == "/posts/:slug" {
			dynamic = r
		}
	}
	if !reflect.DeepEqual(dynamic.Layouts, []string{"example.com/site/app", "example.com/site/app/posts"}) {
		t.Fatal(dynamic.Layouts)
	}
	source := serverSource(p, "id", false)
	if !strings.Contains(source, "//go:embed all:assets") {
		t.Fatal("underscore-prefixed runtime assets must be embedded")
	}
	if !strings.Contains(source, ".NotFound") || !strings.Contains(source, ".Handle") || !strings.Contains(source, ".Layout") {
		t.Fatal(source)
	}
	configured := strings.Index(source, ".Configure(&config)")
	constructed := strings.Index(source, "nextgo.NewContext(ctx,config)")
	if configured == -1 || constructed <= configured {
		t.Fatal("configuration hook must run before app construction")
	}
	lambda := strings.Index(source, `if os.Getenv("AWS_LAMBDA_RUNTIME_API")`)
	if initialized := strings.Index(source, "signal.NotifyContext"); initialized < 0 || constructed <= initialized || lambda <= initialized || !strings.Contains(source, "serverless.Run(ctx,app)") {
		t.Fatal("construction and Lambda runtime must use the initialized shutdown context")
	}
	if strings.Contains(source, "app.Close(shutdown)") || strings.Count(source, "app.Close(drain)") != 1 {
		t.Fatal("generated server must close its application with one independent cleanup context")
	}
	if listening := strings.Index(source, "server.ListenAndServe()"); listening <= lambda {
		t.Fatal("Lambda runtime must be selected before starting an HTTP listener")
	}
	if err = writeGo(filepath.Join(root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}
func TestDiscoveryRejectsAmbiguityAndReservedPaths(t *testing.T) {
	for _, files := range []map[string]string{
		{"app/param_id/page.go": "package id", "app/param_name/page.go": "package name"},
		{"app/page.go": "package app", "app/route.go": "package app"},
		{"app/api/page.go": "package api"}, {"app/assets/page.go": "package assets"},
		{"app/group_a/page.go": "package a", "app/group_b/page.go": "package b"},
		{"app/docs/page.go": "package docs", "app/docs/optional_path/page.go": "package path"},
		{"app/docs/catchall_path/page.go": "package path", "app/docs/optional_rest/page.go": "package rest"},
	} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatalf("accepted %v", files)
		}
	}
}

func TestRouteGroupCatchallPatterns(t *testing.T) {
	for _, test := range []struct{ directory, pattern string }{
		{"group_marketing", "/"},
		{"group_marketing/about", "/about"},
		{"group_marketing/blog/group_content/param_slug", "/blog/:slug"},
		{"docs/catchall_slug", "/docs/*slug"},
		{"docs/optional_slug", "/docs/*slug?"},
		{"group_marketing/catchall_slug/group_content", "/*slug"},
	} {
		t.Run(test.directory, func(t *testing.T) {
			got, err := Pattern(test.directory)
			if err != nil || got != test.pattern {
				t.Fatalf("Pattern(%q) = %q, %v; want %q", test.directory, got, err, test.pattern)
			}
		})
	}
	for _, invalid := range []string{"group_", "docs/catchall_", "docs/optional_", "docs/catchall_slug/details", "docs/optional_slug/param_id", "param_slug/catchall_slug"} {
		if _, err := Pattern(invalid); err == nil {
			t.Errorf("accepted invalid route convention %q", invalid)
		}
	}
}

func TestDiscoveryInheritsBoundariesAndStableLayoutIDs(t *testing.T) {
	root := fixture(t, map[string]string{
		"app/page.go":                                     "package app",
		"app/layout.go":                                   "package app",
		"app/notfound.go":                                 "package app",
		"app/error.go":                                    "package app",
		"app/loading.go":                                  "package app",
		"app/group_content/layout.go":                     "package content",
		"app/group_content/posts/layout.go":               "package posts",
		"app/group_content/posts/notfound.go":             "package posts",
		"app/group_content/posts/error.go":                "package posts",
		"app/group_content/posts/param_slug/page.go":      "package slug",
		"app/group_content/posts/param_slug/loading.go":   "package slug",
		"app/group_content/docs/catchall_path/page.go":    "package path",
		"app/group_content/guide/optional_path/page.go":   "package path",
		"app/api/health/route.go":                         "package health",
		"app/group_content/posts/param_slug/api/route.go": "package api",
	})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range p.Routes {
		switch route.Pattern {
		case "/posts/:slug":
			want := []string{"example.com/site/app", "example.com/site/app/group_content", "example.com/site/app/group_content/posts"}
			if !reflect.DeepEqual(route.Layouts, want) || !reflect.DeepEqual(route.LayoutIDs, want) {
				t.Fatalf("layout chain or identities lost route groups: %+v", route)
			}
			if route.NotFound != "example.com/site/app/group_content/posts" || route.Error != route.NotFound || route.Loading != "example.com/site/app/group_content/posts/param_slug" {
				t.Fatalf("nearest boundaries were not inherited: %+v", route)
			}
		case "/docs/*path", "/guide/*path?", "/":
			if route.NotFound != "example.com/site/app" || route.Error != route.NotFound || route.Loading != route.NotFound {
				t.Fatalf("root boundaries were not inherited: %+v", route)
			}
		case "/api/health", "/posts/:slug/api":
			if route.NotFound != "" || route.Error != "" || route.Loading != "" || len(route.LayoutIDs) != 0 {
				t.Fatalf("API route should bypass page boundaries/layouts: %+v", route)
			}
		default:
			t.Fatalf("unexpected route %+v", route)
		}
	}
	source := serverSource(p, "id", false)
	for _, fragment := range []string{"LayoutIDs:[]string{", ".NotFound", ".Error", ".Loading", "example.com/site/app/group_content/posts", `Pattern:"/docs/*path"`, `Pattern:"/guide/*path?"`} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("generated entrypoint omitted %q: %s", fragment, source)
		}
	}
	if err := writeGo(filepath.Join(root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}
func TestFingerprintDetectsAddDeleteAndPublicChanges(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "public/x.txt": "one"})
	initial, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "public/x.txt"), []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	next, _ := Fingerprint(root)
	if next == initial {
		t.Fatal("asset edit missed")
	}
	_ = os.MkdirAll(filepath.Join(root, ".nextgo"), 0755)
	_ = os.WriteFile(filepath.Join(root, ".nextgo/generated.go"), []byte("ignored"), 0644)
	ignored, _ := Fingerprint(root)
	if ignored != next {
		t.Fatal("output must be excluded")
	}
	_ = os.Remove(filepath.Join(root, "public/x.txt"))
	deleted, _ := Fingerprint(root)
	if deleted == next {
		t.Fatal("deletion missed")
	}
}

func TestFingerprintIncludesHiddenAndNormallyIgnoredPublicDirectories(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "public/.well-known/site.json": "one", "public/dist/app.css": "one"})
	previous, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public/.well-known/site.json", "public/dist/app.css"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("two"), 0644); err != nil {
			t.Fatal(err)
		}
		next, err := Fingerprint(root)
		if err != nil || next == previous {
			t.Fatalf("public asset edit missed for %s: %v", name, err)
		}
		previous = next
	}
	if err := os.Symlink("missing", filepath.Join(root, "public", "link")); err != nil {
		t.Fatal(err)
	}
	next, err := Fingerprint(root)
	if err != nil || next == previous {
		t.Fatalf("invalid public symlink addition missed: %v", err)
	}
	if err := copyTree(filepath.Join(root, "public"), filepath.Join(t.TempDir(), "assets")); err == nil {
		t.Fatal("public symlink should be rejected by the build")
	}
}

func TestFingerprintExcludesGeneratedFilesUnderPublicAncestor(t *testing.T) {
	original := fixture(t, map[string]string{"app/page.go": "package app"})
	root := filepath.Join(t.TempDir(), "public", "project")
	if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, root); err != nil {
		t.Fatal(err)
	}
	initial, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(root, ".nextgo", "generated.go")
	if err := os.MkdirAll(filepath.Dir(generated), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated, []byte("ignored generated source"), 0644); err != nil {
		t.Fatal(err)
	}
	next, err := Fingerprint(root)
	if err != nil || next != initial {
		t.Fatalf("ancestor public directory made generated files affect the fingerprint: %v", err)
	}
}

func TestFingerprintDetectsRootPublicSymlink(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	initial, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(root, "public")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), public); err != nil {
		t.Fatal(err)
	}
	next, err := Fingerprint(root)
	if err != nil || next == initial {
		t.Fatalf("root public symlink addition was not detected: %v", err)
	}
	if err := copyTree(public, filepath.Join(t.TempDir(), "assets")); err == nil {
		t.Fatal("root public symlink should be rejected by the build")
	}
}

func TestDiscoveryTemplatesAreInsideLayoutsAndRespectGroups(t *testing.T) {
	root := fixture(t, map[string]string{
		"app/layout.go": "package app", "app/template.go": "package app",
		"app/group_content/posts/layout.go": "package posts", "app/group_content/posts/template.go": "package posts",
		"app/group_content/posts/param_slug/page.go":     "package slug",
		"app/group_content/posts/param_slug/template.go": "package slug",
		"app/endpoint/route.go":                          "package endpoint",
	})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var page Route
	for _, route := range p.Routes {
		if route.API {
			if len(route.Layouts) != 0 || len(route.Templates) != 0 {
				t.Fatal("endpoint inherited wrappers")
			}
		} else {
			page = route
		}
	}
	want := []string{"example.com/site/app", "example.com/site/app", "example.com/site/app/group_content/posts", "example.com/site/app/group_content/posts", "example.com/site/app/group_content/posts/param_slug"}
	if !reflect.DeepEqual(page.Layouts, want) || !reflect.DeepEqual(page.Templates, map[int]string{1: "/", 3: "/posts", 4: "/posts/:slug"}) {
		t.Fatalf("%+v", page)
	}
	if !reflect.DeepEqual(page.LayoutIDs, []string{want[0], "", want[2], "", ""}) {
		t.Fatal(page.LayoutIDs)
	}
	source := serverSource(p, "test", false)
	if strings.Count(source, "nextgo.Template(") != 3 || !strings.Contains(source, `"/posts/:slug"`) {
		t.Fatal(source)
	}
	if err := writeGo(filepath.Join(root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}

func TestStaticParameterDiscoveryAndRegistration(t *testing.T) {
	root := fixture(t, map[string]string{"app/items/param_id/page.go": "package item", "app/items/param_id/staticparams.go": "package item"})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 1 || !p.Routes[0].StaticParams {
		t.Fatalf("missing enumerator: %+v", p.Routes)
	}
	source := serverSource(p, "test", false)
	if !strings.Contains(source, "StaticParams:p0.StaticParams") || !strings.Contains(source, "app.StaticPathsForDomain(enumeration,*exportDomain)") || !strings.Contains(source, "static-paths") {
		t.Fatal("generated executable must register and expose build enumeration")
	}
	for _, files := range []map[string]string{
		{"app/page.go": "package app", "app/staticparams.go": "package app"},
		{"app/api/param_id/route.go": "package api", "app/api/param_id/staticparams.go": "package api"},
		{"app/page.go": "package app", "app/items/param_id/staticparams.go": "package item"},
	} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatal("accepted orphan/static/API parameter enumeration")
		}
	}
}

func TestDiscoveryRegistersAccessBoundaryAncestryAndSafeLayoutPrefixes(t *testing.T) {
	root := fixture(t, map[string]string{
		"app/page.go": "package app", "app/layout.go": "package app", "app/unauthorized.go": "package app", "app/forbidden.go": "package app",
		"app/group_staff/template.go": "package staff", "app/group_staff/unauthorized.go": "package staff",
		"app/group_staff/restricted/layout.go": "package restricted", "app/group_staff/restricted/forbidden.go": "package restricted", "app/group_staff/restricted/page.go": "package restricted",
		"app/group_staff/restricted/api/route.go": "package api",
	})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Unauthorized != "example.com/site/app" || p.Forbidden != p.Unauthorized {
		t.Fatal("root access conventions", p)
	}
	for _, r := range p.Routes {
		if r.Pattern == "/restricted" {
			want := []AccessBoundary{{Layouts: 1, Unauthorized: "example.com/site/app/group_staff"}, {Layouts: 2, Forbidden: "example.com/site/app/group_staff/restricted"}}
			if !reflect.DeepEqual(r.Access, want) {
				t.Fatalf("boundary chain %+v want %+v", r.Access, want)
			}
		} else if len(r.Access) != 0 {
			t.Fatal("API/root inherited nested UI", r)
		}
	}
	source := serverSource(p, "id", false)
	for _, value := range []string{"Access:[]nextgo.AccessBoundary{", "{Layouts:1,Unauthorized:", "{Layouts:2,Forbidden:", ".Unauthorized", ".Forbidden"} {
		if !strings.Contains(source, value) {
			t.Fatalf("missing generated %s: %s", value, source)
		}
	}
	if err := writeGo(filepath.Join(root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataConventionBuildsOrderedServerOnlyLayers(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "app/layout.go": "package app", "app/metadata.go": "package app", "app/group_blog/metadata.go": "package blog", "app/group_blog/template.go": "package blog", "app/group_blog/posts/metadata.go": "package posts", "app/group_blog/posts/layout.go": "package posts", "app/group_blog/posts/param_slug/page.go": "package slug", "app/group_blog/posts/param_slug/metadata.go": "package slug", "app/api/health/route.go": "package health", "app/api/health/metadata.go": "package health"})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Routes {
		if r.Pattern == "/posts/:slug" {
			want := []MetadataLayer{{Import: "example.com/site/app"}, {Import: "example.com/site/app/group_blog", Layouts: 1}, {Import: "example.com/site/app/group_blog/posts", Layouts: 2}, {Import: "example.com/site/app/group_blog/posts/param_slug", Layouts: 3}}
			if !reflect.DeepEqual(r.Metadata, want) {
				t.Fatalf("metadata chain %+v want %+v", r.Metadata, want)
			}
		}
		if r.API && len(r.Metadata) != 0 {
			t.Fatal("API inherited metadata", r)
		}
	}
	source := serverSource(p, "id", false)
	if !strings.Contains(source, "Metadata:[]nextgo.MetadataLayer{") || !strings.Contains(source, ".Metadata,Layouts:3}") {
		t.Fatal("missing metadata registration", source)
	}
	if err := writeGo(filepath.Join(root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}

func TestParallelDiscoveryAndGeneratedImports(t *testing.T) {
	root := fixture(t, map[string]string{"app/layout.go": "package app", "app/dash/layout.go": "package dash", "app/dash/page.go": "package dash", "app/dash/chart/page.go": "package chart", "app/dash/slot_panel/default.go": "package panel", "app/dash/slot_panel/chart/page.go": "package chart"})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 2 || len(p.Routes[0].Slots) != 1 {
		t.Fatal(p.Routes)
	}
	slot := p.Routes[0].Slots[0]
	if slot.Name != "panel" || slot.Layout != 1 || slot.Base != "/dash" || slot.Default != "example.com/site/app/dash/slot_panel" || len(slot.Routes) != 1 || slot.Routes[0].Pattern != "/dash/chart" {
		t.Fatal(slot)
	}
	source := serverSource(p, "parallel-build", false)
	if !strings.Contains(source, "Slots:[]nextgo.Slot") || !strings.Contains(source, ".Default,") || !strings.Contains(source, `"example.com/site/app/dash/slot_panel/chart"`) {
		t.Fatal(source)
	}
	if err := writeGo(filepath.Join(root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}

func TestParallelDiscoveryRejectsInvalidOwnershipAndBranches(t *testing.T) {
	for _, files := range []map[string]string{
		{"app/page.go": "package app", "app/slot_panel/default.go": "package panel"},
		{"app/page.go": "package app", "app/layout.go": "package app", "app/slot_children/default.go": "package child"},
		{"app/page.go": "package app", "app/layout.go": "package app", "app/slot_panel/route.go": "package panel"},
		{"app/page.go": "package app", "app/layout.go": "package app", "app/slot_panel/page.go": "package panel", "app/slot_panel/slot_nested/default.go": "package nested"},
	} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatal("invalid slot tree accepted", files)
		}
	}
}

func TestPrimarySlotDefaultsAndSlotOnlyComposition(t *testing.T) {
	for _, withDefault := range []bool{false, true} {
		files := map[string]string{"app/layout.go": "package app", "app/metadata.go": "package app", "app/dash/layout.go": "package dash", "app/dash/page.go": "package dash", "app/dash/slot_panel/other/page.go": "package other"}
		if withDefault {
			files["app/dash/default.go"] = "package dash"
		}
		p, err := Discover(fixture(t, files))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Routes) != 2 {
			t.Fatal(p.Routes)
		}
		for _, r := range p.Routes {
			if r.Primary == nil || r.Primary.Layout != 1 || r.Primary.Base != "/dash" || len(r.Slots) != 1 {
				t.Fatal(r)
			}
			if r.Pattern == "/dash/other" && (!r.Primary.Fallback || r.Default != withDefault || r.Missing == withDefault || len(r.Metadata) != 1 || len(r.Layouts) != 2) {
				t.Fatal(r)
			}
		}
		source := serverSource(p, "test", false)
		if !strings.Contains(source, "Primary:&nextgo.PrimarySlot") {
			t.Fatal(source)
		}
		if withDefault && !strings.Contains(source, ".Default,") || !withDefault && !strings.Contains(source, "nextgo.ErrNotFound") {
			t.Fatal(source)
		}
	}
}
func TestSlotOnlyApplicationWithoutAnyPrimaryPage(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{"app/layout.go": "package app", "app/default.go": "package app", "app/slot_panel/chart/page.go": "package chart"}))
	if err != nil || len(p.Routes) != 1 || !p.Routes[0].Primary.Fallback || !p.Routes[0].Default {
		t.Fatal(p, err)
	}
}
func TestPrimaryDefaultsRequireAnOwningParallelLayout(t *testing.T) {
	_, err := Discover(fixture(t, map[string]string{"app/layout.go": "package app", "app/page.go": "package app", "app/default.go": "package app"}))
	if err == nil {
		t.Fatal("orphan primary default accepted")
	}
}

func TestPrimaryCompositionAttachesOuterNamedSlotsAfterDiscoveringAllURLs(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{"app/layout.go": "package app", "app/slot_banner/default.go": "package banner", "app/boards/layout.go": "package boards", "app/boards/default.go": "package boards", "app/boards/slot_team/activity/page.go": "package activity"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 1 || len(p.Routes[0].Slots) != 2 || p.Routes[0].Slots[0].Name != "banner" || p.Routes[0].Slots[1].Name != "team" || p.Routes[0].Primary.Layout != 1 {
		t.Fatal(p.Routes)
	}
}
func TestPrimaryCompositionRejectsForeignOwnershipAndIgnoredDefaults(t *testing.T) {
	for _, files := range []map[string]string{
		{"app/page.go": "package app", "app/default.go": "package app"},
		{"app/group_one/layout.go": "package one", "app/group_one/page.go": "package one", "app/group_one/slot_panel/foo/page.go": "package foo", "app/group_two/foo/page.go": "package foo"},
		{"app/layout.go": "package app", "app/page.go": "package app", "app/slot_panel/page.go": "package panel", "app/slot_panel/inner/layout.go": "package inner", "app/slot_panel/inner/default.go": "package inner"},
	} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatal("unsupported ownership/default ignored", files)
		}
	}
}

func TestNamedDefaultDiscoveryUsesCompleteSlotRootAncestry(t *testing.T) {
	for _, layout := range []bool{false, true} {
		files := map[string]string{"app/layout.go": "package app", "app/page.go": "package app", "app/dash/layout.go": "package dash", "app/dash/page.go": "package dash", "app/dash/slot_panel/default.go": "package panel", "app/dash/slot_panel/template.go": "package panel", "app/dash/slot_panel/metadata.go": "package panel", "app/dash/slot_panel/error.go": "package panel", "app/dash/slot_panel/notfound.go": "package panel"}
		if layout {
			files["app/dash/slot_panel/layout.go"] = "package panel"
		}
		p, err := Discover(fixture(t, files))
		if err != nil {
			t.Fatal(err)
		}
		var fallback *Route
		for _, r := range p.Routes {
			if r.Pattern == "/dash" {
				fallback = r.Slots[0].DefaultRoute
			}
		}
		count := 1
		if layout {
			count = 2
		}
		if fallback == nil || !fallback.Default || fallback.Pattern != "/dash" || len(fallback.Layouts) != count || len(fallback.Metadata) != 1 || fallback.Error != "example.com/site/app/dash/slot_panel" || fallback.NotFound != fallback.Error || fallback.Templates[count-1] != "/dash" {
			t.Fatal(fallback)
		}
		source := serverSource(p, "default-route", false)
		if !strings.Contains(source, "DefaultRoute:&nextgo.Route") || !strings.Contains(source, ".Default,") || !strings.Contains(source, `nextgo.Template("example.com/site/app/dash/slot_panel","/dash"`) {
			t.Fatal(source)
		}
		if err := writeGo(filepath.Join(p.Root, "generated.go"), source); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedNamedSlotsGenerateLogicalCompositeURLsAndDefaults(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{
		"app/layout.go": "package app", "app/page.go": "package app", "app/default.go": "package app",
		"app/slot_panel/layout.go": "package panel", "app/slot_panel/default.go": "package panel", "app/slot_panel/chart/page.go": "package chart",
		"app/slot_panel/slot_detail/default.go": "package detail", "app/slot_panel/slot_detail/drill/page.go": "package drill",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 3 {
		t.Fatal(p.Routes)
	}
	var route *Route
	for i := range p.Routes {
		if p.Routes[i].Pattern == "/drill" {
			route = &p.Routes[i]
		}
	}
	if route == nil || route.Primary == nil || len(route.Slots) != 1 {
		t.Fatal(route)
	}
	panel := route.Slots[0]
	if panel.DefaultRoute == nil || panel.DefaultRoute.Primary == nil || len(panel.DefaultRoute.Slots) != 1 || len(panel.Routes) != 2 {
		t.Fatal(panel)
	}
	for _, branch := range panel.Routes {
		if branch.Pattern != "/drill" && branch.Pattern != "/chart" {
			t.Fatal(branch)
		}
		if branch.Primary == nil || len(branch.Slots) != 1 || branch.Slots[0].Base != "/" || branch.Slots[0].Routes[0].Pattern != "/drill" {
			t.Fatal(branch)
		}
	}
	source := serverSource(p, "nested", false)
	if !strings.Contains(source, `"example.com/site/app/slot_panel/slot_detail/drill"`) {
		t.Fatal(source)
	}
	if err := writeGo(filepath.Join(p.Root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}

func TestNestedSlotReservedPathsUseGlobalURLsAndRejectRepeatedParameters(t *testing.T) {
	valid := map[string]string{"app/layout.go": "package app", "app/page.go": "package app", "app/dash/layout.go": "package dash", "app/dash/default.go": "package dash", "app/dash/slot_panel/default.go": "package panel", "app/dash/slot_panel/api/page.go": "package api"}
	p, err := Discover(fixture(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range p.Routes {
		found = found || r.Pattern == "/dash/api"
	}
	if !found {
		t.Fatal(p.Routes)
	}
	_, err = Discover(fixture(t, map[string]string{"app/layout.go": "package app", "app/page.go": "package app", "app/param_id/layout.go": "package account", "app/param_id/default.go": "package account", "app/param_id/slot_panel/param_id/page.go": "package detail"}))
	if err == nil {
		t.Fatal("repeated parameter in composed logical URL accepted")
	}
}

func TestMultiplePrimaryOwnersGenerateOneOrderedChain(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{
		"app/layout.go": "package app", "app/page.go": "package app", "app/default.go": "package app", "app/slot_banner/default.go": "package banner",
		"app/reports/layout.go": "package reports", "app/reports/page.go": "package reports", "app/reports/default.go": "package reports",
		"app/reports/slot_detail/default.go": "package detail", "app/reports/slot_detail/summary/page.go": "package summary",
	}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range p.Routes {
		if r.Pattern == "/reports/summary" {
			found = true
			if r.Primary != nil || len(r.Primaries) != 2 || r.Primaries[0].Layout != 0 || r.Primaries[1].Layout != 1 || r.Primaries[0].Fallback || !r.Primaries[1].Fallback || r.Import != "example.com/site/app/reports" || !r.Default {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal(p.Routes)
	}
	source := serverSource(p, "chain", false)
	if !strings.Contains(source, "Primaries:[]nextgo.PrimarySlot") {
		t.Fatal(source)
	}
	if err := writeGo(filepath.Join(p.Root, "generated.go"), source); err != nil {
		t.Fatal(err)
	}
}

func TestParallelURLContributorsComposeTheDeepestCompatibleOwner(t *testing.T) {
	files := map[string]string{
		"app/layout.go": "package app", "app/page.go": "package app", "app/default.go": "package app",
		"app/slot_banner/default.go": "package banner", "app/slot_banner/reports/summary/page.go": "package summary",
		"app/reports/layout.go": "package reports", "app/reports/default.go": "package reports",
		"app/reports/slot_detail/default.go": "package detail", "app/reports/slot_detail/summary/page.go": "package summary",
	}
	p, err := Discover(fixture(t, files))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range p.Routes {
		if r.Pattern == "/reports/summary" {
			found = true
			if r.Import != "example.com/site/app/reports" || len(r.Primaries) != 2 || len(r.Slots) != 2 || !r.Primaries[1].Fallback {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal(p.Routes)
	}
}

func TestCompositeStaticURLWinsOverLessSpecificPrimaryRoute(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{
		"app/layout.go": "package app", "app/page.go": "package app",
		"app/param_one/param_two/page.go": "package generic",
		"app/reports/layout.go":           "package reports", "app/reports/default.go": "package reports",
		"app/reports/slot_detail/summary/page.go": "package summary",
	}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range p.Routes {
		if r.Pattern == "/reports/summary" {
			found = true
			if r.Import != "example.com/site/app/reports" {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal(p.Routes)
	}
}
