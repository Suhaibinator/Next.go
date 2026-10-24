package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMutationContractsCompileForBothTargetsWithoutNativeHandlerLeak(t *testing.T) {
	root := fixture(t, map[string]string{
		"app/page.go": "package app",
		"schema/contract.go": `package schema
import "github.com/Suhaibinator/Next.go/mutation"
type Input struct { Amount int ` + "`json:\"amount\"`" + ` }
type Output struct { Count int ` + "`json:\"count\"`" + ` }
var Save=mutation.Must[Input,Output]("save","v1")
`,
		"schema/server.go": `//go:build !js
package schema
import("github.com/Suhaibinator/Next.go/actions";"github.com/Suhaibinator/Next.go/mutation";_ "github.com/Suhaibinator/Next.go/serveronly")
func Bind(router *actions.Router){Save.Bind(router,func(c *actions.Context,in Input)(mutation.Result[Output],error){return mutation.Result[Output]{Data:Output{Count:in.Amount}},nil})}
`,
		"consumer/main_js.go": `//go:build js && wasm
package main
import("context";"example.com/site/schema")
func main(){response,err:=schema.Save.Call(context.Background(),schema.Input{Amount:2});if err==nil{println(response.Data.Count)}}
`,
	})
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/site\ngo 1.24\nrequire " + framework + " v0.0.0\nreplace " + framework + " => " + strconv.Quote(repo) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(env []string, args ...string) ([]byte, error) {
		c := exec.Command("go", args...)
		c.Dir = root
		c.Env = append(c.Environ(), append([]string{"GOWORK=off"}, env...)...)
		return c.CombinedOutput()
	}
	if output, err := run(nil, "test", "./schema"); err != nil {
		t.Fatalf("native binding: %v\n%s", err, output)
	}
	if output, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "build", "-o", filepath.Join(root, "consumer.wasm"), "./consumer"); err != nil {
		t.Fatalf("WASM caller: %v\n%s", err, output)
	}
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBrowserGraph(context.Background(), p, filepath.Join(root, "consumer")); err != nil {
		t.Fatal(err)
	}
	deps, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "list", "-deps", "./consumer")
	if err != nil {
		t.Fatal(err, string(deps))
	}
	for _, dep := range strings.Fields(string(deps)) {
		if dep == framework+"/actions" || dep == framework+"/serveronly" || dep == "net/http" {
			t.Fatal("native implementation leaked into browser graph", dep)
		}
	}
	bad := filepath.Join(root, "schema", "wrong_server.go")
	if err := os.WriteFile(bad, []byte(`//go:build !js
package schema
import("github.com/Suhaibinator/Next.go/actions";"github.com/Suhaibinator/Next.go/mutation")
func Wrong(r *actions.Router){Save.Bind(r,func(*actions.Context,Input)(mutation.Result[string],error){return mutation.Result[string]{},nil})}
`), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(nil, "test", "./schema"); err == nil || !strings.Contains(string(output), "build failed") {
		t.Fatalf("wrong output binding accepted: %v\n%s", err, output)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "consumer", "main_js.go"), []byte(`//go:build js && wasm
package main
import("context";"example.com/site/schema")
func main(){schema.Save.Call(context.Background(),"wrong input")}
`), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := run([]string{"GOOS=js", "GOARCH=wasm"}, "build", "-o", filepath.Join(root, "bad.wasm"), "./consumer"); err == nil || !strings.Contains(string(output), "cannot use") {
		t.Fatalf("wrong client input accepted: %v\n%s", err, output)
	}
}
