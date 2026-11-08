package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateAppDirectoriesExcludeEveryRoutingConvention(t *testing.T) {
	files := map[string]string{"app/page.go": "package app", "app/public/page.go": "package public"}
	for _, name := range []string{"private_helpers", "_helpers", "group_public/private_nested", "group_public/_nested"} {
		for _, file := range []string{"page.go", "route.go", "layout.go", "template.go", "metadata.go", "error.go", "notfound.go", "unauthorized.go", "forbidden.go", "loading.go", "staticparams.go", "prerender.go", "regions.go", "default.go", "slot_bad/page.go", "slot_bad/intercept_root/page.go"} {
			files["app/"+name+"/"+file] = "intentionally not parsed as a routing declaration"
		}
	}
	root := fixture(t, files)
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 2 || p.NotFound != "" || p.Default != "" {
		t.Fatal("private subtree affected routing", p.Routes)
	}
	for _, route := range p.Routes {
		if route.Pattern != "/" && route.Pattern != "/public" || len(route.Layouts) > 0 || len(route.Metadata) > 0 || len(route.Slots) > 0 {
			t.Fatal(route)
		}
	}
	source, err := TypedRoutesSource(p)
	if err != nil || strings.Contains(string(source), "Private") || strings.Contains(string(source), "Helpers") {
		t.Fatal("private typed route generated", err, string(source))
	}
	for _, path := range []string{"private_helpers", "_helpers/child", "public/private_child/page", "group_public/_hidden"} {
		if _, err := Pattern(path); err == nil {
			t.Fatal("private directory converted into public URL", path)
		}
	}
	for path, want := range map[string]string{"literal__helpers": "/_helpers", "literal_private_helpers/child": "/private_helpers/child", "literal_param_id": "/param_id", "literal_group_name": "/group_name"} {
		if got, err := Pattern(path); err != nil || got != want {
			t.Fatal("literal escape", path, got, err)
		}
	}
	if _, err := Pattern("literal_"); err == nil {
		t.Fatal("empty literal route accepted")
	}
}

func TestPrivateRoutesRemainBuildAndWatchInputs(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "app/private_helpers/data.go": "package helpers\nconst PublicMessage=\"first\"\n", "app/_native/data.go": "package native\nconst PublicMessage=\"first\"\n"})
	snapshot := func() string {
		t.Helper()
		hash, err := hashInputsContext(context.Background(), inputPlan{Roots: []string{root}}, "")
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	before := snapshot()
	for _, name := range []string{"private_helpers", "_native"} {
		path := filepath.Join(root, "app", name, "data.go")
		if err := os.WriteFile(path, []byte("package helpers\nconst PublicMessage=\"changed\"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		after := snapshot()
		if before == after {
			t.Fatal("private helper source omitted from generation identity/watch snapshot", name)
		}
		before = after
	}
}
