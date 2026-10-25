package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePublishesAndChecksActionReferences(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/site\ngo 1.24\n",
		"app/page.go": "package app\n",
		"actions/save/contract.go": `package save
import "github.com/Suhaibinator/Next.go/mutation"
type Input struct{ Name string }
type Output struct{ Name string }
var Contract=mutation.Must[Input,Output]("save","v1")
`,
		"actions/save/action.go": `//go:build !js

package save
import("github.com/Suhaibinator/Next.go/actions";"github.com/Suhaibinator/Next.go/mutation";_ "github.com/Suhaibinator/Next.go/serveronly")
func Handle(*actions.Context,Input)(mutation.Result[Output],error){return mutation.Result[Output]{},nil}
func Options()mutation.BindOptions[Input]{return mutation.BindOptions[Input]{}}
`,
	}
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := execute(context.Background(), []string{"generate", "--check", root}); err == nil {
		t.Fatal("check accepted missing outputs")
	}
	if _, err := os.Stat(filepath.Join(root, "actionrefs")); !os.IsNotExist(err) {
		t.Fatal("check wrote outputs")
	}
	if err := execute(context.Background(), []string{"generate", root}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"routes/nextgo_gen.go", "actionrefs/nextgo_gen.go", "actionrefs/nextgo_gen_js.go", "actionrefs/nextgo_gen_server.go"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := execute(context.Background(), []string{"generate", "--check", root}); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(root, "actions/save/contract.go")
	source := strings.Replace(files["actions/save/contract.go"], `"v1"`, `"v2"`, 1)
	if err := os.WriteFile(contract, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := execute(context.Background(), []string{"generate", "--check", root}); err == nil {
		t.Fatal("check accepted stale action references")
	}
}
