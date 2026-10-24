package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultExportComposesParentParametersInIndependentModule(t *testing.T) {
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "composed")
	if err := executeInit(context.Background(), []string{"-module", "example.test/composed", "-framework", framework, root}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"app/config.go": `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Configure(c *nextgo.Config){c.BasePath="/composed";c.TrailingSlash=true;c.Locales=&nextgo.LocaleConfig{Codes:[]string{"en","fr"},Default:"en",PrefixDefault:true}}
`,
		"app/cats/param_cat/staticparams.go": `//go:build !js
package category
import("context";nextgo "github.com/Suhaibinator/Next.go")
func StaticParams(ctx context.Context)([]nextgo.Params,error){return []nextgo.Params{{"cat":nextgo.CurrentLocale(ctx)+"-café"}},nil}
`,
		"app/cats/param_cat/summary/page.go": `//go:build !js
package summary
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(c *nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Inherited summary",Body:ui.Text("Summary "+c.Params["cat"])},nil}
`,
		"app/cats/param_cat/param_item/staticparams.go": `//go:build !js
package item
import("context";"fmt";nextgo "github.com/Suhaibinator/Next.go")
func StaticParams(ctx context.Context)([]nextgo.Params,error){p:=nextgo.StaticParentParams(ctx);if p["cat"]!=nextgo.CurrentLocale(ctx)+"-café"{return nil,fmt.Errorf("wrong parent scope")};return []nextgo.Params{{"item":"a,b"}},nil}
`,
		"app/cats/param_cat/param_item/page.go": `//go:build !js
package item
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(c *nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Composed child",Body:ui.Text(c.Params["cat"]+" / "+c.Params["item"])},nil}
`,
	}
	for file, source := range files {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "static")
	if err := executeExport(context.Background(), []string{"-out", output, root}); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "fr"} {
		for _, child := range []string{"summary", "a,b"} {
			path := filepath.Join(output, "composed", locale, "cats", locale+"-café", child, "index.html")
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), locale+"-café") {
				t.Fatal(path, err)
			}
			path = filepath.Join(output, "composed", locale, "_nextgo/data/cats", locale+"-café", child+".json")
			data, err = os.ReadFile(path)
			var envelope struct {
				Status    int
				URL, HTML string
			}
			if err != nil || json.Unmarshal(data, &envelope) != nil || envelope.Status != 200 || !strings.Contains(envelope.HTML, locale+"-café") || !strings.HasSuffix(envelope.URL, "/") {
				t.Fatal(path, string(data), err)
			}
		}
	}
	// A parent-binding conflict must not publish over the previous export.
	before, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(files["app/cats/param_cat/param_item/staticparams.go"], `{"item":"a,b"}`, `{"cat":"different-parent","item":"a,b"}`, 1)
	if err := os.WriteFile(filepath.Join(root, "app/cats/param_cat/param_item/staticparams.go"), []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}
	if err := executeExport(context.Background(), []string{"-out", output, root}); err == nil {
		t.Fatal("conflicting parent binding exported")
	}
	after, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("failed composition replaced prior export", err)
	}
}
