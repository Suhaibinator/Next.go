package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultExportEnumeratesDynamicPagesInIndependentModule(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	framework, err := filepath.Abs(filepath.Join(cwd, "../.."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "independent")
	if err := executeInit(context.Background(), []string{"-module", "example.test/enumerated", "-framework", framework, root}); err != nil {
		t.Fatal(err)
	}
	configSource := `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Configure(c *nextgo.Config){c.BasePath="/mounted";c.AssetPrefix="/cdn"}
`
	if err := os.WriteFile(filepath.Join(root, "app/config.go"), []byte(configSource), 0644); err != nil {
		t.Fatal(err)
	}
	source := `//go:build !js
package name
import("context";nextgo "github.com/Suhaibinator/Next.go")
func StaticParams(ctx context.Context)([]nextgo.Params,error){if err:=ctx.Err();err!=nil{return nil,err};return []nextgo.Params{{"name":"Ada"},{"name":"café"},{"name":"a,b"}},nil}
`
	if err := os.WriteFile(filepath.Join(root, "app/hello/param_name/staticparams.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "static")
	if err := executeExport(context.Background(), []string{"-out", output, root}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Ada", "café", "a,b"} {
		data, err := os.ReadFile(filepath.Join(output, "mounted", "hello", name, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Hello, "+name+"!") || !strings.Contains(string(data), `data-nextgo-export="true"`) {
			t.Fatalf("missing enumerated SSR: %s", data)
		}
		data, err = os.ReadFile(filepath.Join(output, "mounted", "_nextgo/data/hello", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Version, Status int
			HTML            string
		}
		if err := json.Unmarshal(data, &envelope); err != nil || envelope.Version != 3 || envelope.Status != 200 || !strings.Contains(envelope.HTML, name) {
			t.Fatalf("invalid enumerated navigation data: %s %v", data, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exported exportManifest
	if err := json.Unmarshal(manifest, &exported); err != nil {
		t.Fatal(err)
	}
	expected := []string{"/", "/about", "/hello/Ada", "/hello/a,b", "/hello/caf%C3%A9"}
	if exported.BasePath != "/mounted" || exported.AssetPrefix != "/cdn" {
		t.Fatalf("deployment metadata: %+v", exported)
	}
	if _, err := os.Stat(filepath.Join(output, "cdn/assets/style.css")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.Paths, expected) {
		t.Fatalf("paths=%v", exported.Paths)
	}
}

func TestCappedEnumerationCaptureHandlesIOCopy(t *testing.T) {
	output := &cappedOutput{limit: 3}
	// LimitReader removes the source's WriterTo optimization. The destination
	// must not expose an embedded Buffer.ReadFrom that bypasses its Write cap.
	n, err := io.Copy(output, io.LimitReader(strings.NewReader(strings.Repeat("x", 4096)), 4096))
	if err != nil || n != 4096 || output.String() != "xxx" || !output.overflow {
		t.Fatalf("capture limit: %d %s %v", n, output.String(), err)
	}
}

func TestLocaleDefaultExportEnumeratesRequestFreeParameters(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	framework, err := filepath.Abs(filepath.Join(cwd, "../.."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "localized")
	if err := executeInit(context.Background(), []string{"-module", "example.test/localized", "-framework", framework, root}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"app/config.go": `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Configure(c *nextgo.Config){c.BasePath="/store";c.AssetPrefix="/shared";c.TrailingSlash=true;c.Locales=&nextgo.LocaleConfig{Codes:[]string{"en","fr"},Default:"en",PrefixDefault:true}}
`,
		"app/hello/param_name/staticparams.go": `//go:build !js
package name
import("context";nextgo "github.com/Suhaibinator/Next.go")
func StaticParams(ctx context.Context)([]nextgo.Params,error){return []nextgo.Params{{"name":nextgo.CurrentLocale(ctx)+"-café"}},nil}
`,
	}
	for file, source := range files {
		if err := os.WriteFile(filepath.Join(root, file), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "site")
	if err := executeExport(context.Background(), []string{"-out", output, root}); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"en", "fr"} {
		file := filepath.Join(output, "store", code, "_nextgo", "data", "hello", code+"-café.json")
		data, err := os.ReadFile(file)
		var envelope struct {
			URL      string
			Document struct {
				Language string `json:"lang"`
			}
			HTML string
		}
		if err != nil || json.Unmarshal(data, &envelope) != nil || envelope.Document.Language != code || envelope.URL != "/store/"+code+"/hello/"+code+"-caf%C3%A9/" || !strings.Contains(envelope.HTML, code+"-café") {
			t.Fatal(file, string(data), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	var manifest exportManifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Format != "nextgo-static-v2" || len(manifest.Paths) != 6 {
		t.Fatal(string(data), err)
	}
}

func TestDomainDefaultExportEnumeratesSelectedHostInIndependentModule(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	framework, err := filepath.Abs(filepath.Join(cwd, "../.."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "domain-app")
	if err := executeInit(context.Background(), []string{"-module", "example.test/domain-export", "-framework", framework, root}); err != nil {
		t.Fatal(err)
	}
	for file, source := range map[string]string{
		"app/config.go": `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Configure(c *nextgo.Config){c.BasePath="/store";c.AssetPrefix="/shared";c.TrailingSlash=true;c.Locales=&nextgo.LocaleConfig{Codes:[]string{"en","fr"},Default:"en",Domains:[]nextgo.LocaleDomain{{Host:"en.example",Default:"en"},{Host:"fr.example:443",Default:"fr"}}}}
`,
		"app/hello/param_name/staticparams.go": `//go:build !js
package name
import("context";"fmt";nextgo "github.com/Suhaibinator/Next.go")
func StaticParams(ctx context.Context)([]nextgo.Params,error){u,err:=nextgo.LocaleURL(ctx,"fr","/about");if err!=nil||u!="https://fr.example/store/about/"{return nil,fmt.Errorf("wrong enumeration host: %s %v",u,err)};return []nextgo.Params{{"name":nextgo.CurrentLocale(ctx)+"-café"}},nil}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "site")
	if err := executeExport(context.Background(), []string{"-out", output, "-domain", "FR.EXAMPLE.:443", root}); err != nil {
		t.Fatal(err)
	}
	for code, prefix := range map[string]string{"en": "en", "fr": ""} {
		file := filepath.Join(output, "store", prefix, "_nextgo", "data", "hello", code+"-café.json")
		data, err := os.ReadFile(file)
		var envelope struct {
			URL      string
			Document struct {
				Language string `json:"lang"`
			}
		}
		wantPrefix := "/store"
		if prefix != "" {
			wantPrefix += "/" + prefix
		}
		if err != nil || json.Unmarshal(data, &envelope) != nil || envelope.Document.Language != code || envelope.URL != wantPrefix+"/hello/"+code+"-caf%C3%A9/" {
			t.Fatal(file, string(data), err)
		}
	}
	before, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	var m exportManifest
	if err != nil || json.Unmarshal(before, &m) != nil || m.Domain != "fr.example" || len(m.Paths) != 6 {
		t.Fatal(string(before), err)
	}
	for _, domain := range []string{"", "unconfigured.example"} {
		if err := executeExport(context.Background(), []string{"-out", output, "-domain", domain, root}); err == nil {
			t.Fatal("missing or unknown domain accepted", domain)
		}
		after, _ := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
		if string(after) != string(before) {
			t.Fatal("rejected domain replaced a publication")
		}
	}
}
