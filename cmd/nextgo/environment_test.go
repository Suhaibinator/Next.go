package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportLoadsProductionEnvironmentWithoutPublishingFiles(t *testing.T) {
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	app := filepath.Join(root, "app")
	if err := os.Mkdir(app, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module environmentfixture\n\ngo 1.24.0\n\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => "+filepath.ToSlash(framework)+"\n")
	write("app/page.go", `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(c *nextgo.Context)(nextgo.Page,error){value,_:=nextgo.PublicValue(c.Context,"message");return nextgo.Page{Body:ui.Text(value)},nil}
`)
	write("app/config.go", `//go:build !js
package app
import("os";nextgo "github.com/Suhaibinator/Next.go")
func Configure(config *nextgo.Config){config.Public=map[string]string{"message":os.Getenv("NEXTGO_ENV_FIXTURE_PUBLIC")}}
`)
	write(".env.production", "NEXTGO_ENV_FIXTURE_PUBLIC=Exported file configuration\nNEXTGO_ENV_FIXTURE_PRIVATE=synthetic-never-ship-431\nAWS_LAMBDA_RUNTIME_API=not-a-real-runtime\n")
	write(".env.development", "NEXTGO_ENV_FIXTURE_PUBLIC=Wrong mode\n")
	output := filepath.Join(t.TempDir(), "export")
	if err := executeExport(context.Background(), []string{"-out", output, root}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Exported file configuration") || strings.Contains(string(data), "Wrong mode") {
		t.Fatal("export configuration missing")
	}
	if _, ok := os.LookupEnv("NEXTGO_ENV_FIXTURE_PRIVATE"); ok {
		t.Fatal("CLI process environment mutated")
	}
	if err := filepath.WalkDir(output, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".env") {
			t.Fatal("exported env file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "synthetic-never-ship-431") {
			t.Fatal("exported private configuration")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
