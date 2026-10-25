package project

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestZonesArtifactBudgetAndIsolation(t *testing.T) {
	source := t.TempDir()
	writeInput(t, source, "nested/file", "public bytes")
	target := filepath.Join(t.TempDir(), "copy")
	budget := &zonesArtifactBudget{}
	if err := copyZoneArtifact(context.Background(), source, target, budget); err != nil {
		t.Fatal(err)
	}
	if budget.bytes != 12 || budget.entries != 3 {
		t.Fatal(budget)
	}
	if data, err := os.ReadFile(filepath.Join(target, "nested", "file")); err != nil || string(data) != "public bytes" {
		t.Fatal(string(data), err)
	}
	for label, budget := range map[string]*zonesArtifactBudget{"bytes": {bytes: maxZonesArtifactBytes - 1}, "entries": {entries: maxZonesArtifactEntries}} {
		t.Run(label, func(t *testing.T) {
			err := copyZoneArtifact(context.Background(), source, filepath.Join(t.TempDir(), "copy"), budget)
			if err == nil || !strings.Contains(err.Error(), "128 MiB") {
				t.Fatal(err)
			}
		})
	}
	if err := os.Symlink(filepath.Join(source, "nested", "file"), filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyZoneArtifact(context.Background(), source, filepath.Join(t.TempDir(), "copy"), &zonesArtifactBudget{}); err == nil {
		t.Fatal("copied symlink")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyZoneArtifact(ctx, source, filepath.Join(t.TempDir(), "copy"), &zonesArtifactBudget{}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestZonesGeneratedSourceSharesImportsAndScopesConfiguration(t *testing.T) {
	p := &Project{Import: "fixture.test/site", Configure: "fixture.test/site/app", Routes: []Route{{Pattern: "/", Import: "fixture.test/site/app"}}, MetadataFiles: []MetadataFile{{Name: "robots.txt", Source: "app/robots.txt"}}}
	zones := []compiledZone{{ZoneManifest{ID: "alpha", Host: "alpha.test"}, p, "runtime-a"}, {ZoneManifest{ID: "beta", Host: "beta.test"}, p, "runtime-a"}}
	for _, dev := range []bool{false, true} {
		source := zonesServerSource(zones, dev)
		if strings.Count(source, `"fixture.test/site/app"`) != 1 {
			t.Fatal("reused app imported twice", source)
		}
		for _, part := range []string{`fs.Sub(embedded,"assets/alpha")`, `fs.Sub(embedded,"assets/beta")`, `fs.Sub(embedded,"private/alpha")`, `fs.Sub(embedded,"assets/beta/metadata")`, `Configure:func(config *nextgo.Config)error`, `.Configure(config)`, `route.Prerender!=nil`, `config.Phase!=phase||config.Dev!=`, `config.PrerenderBuildMode`, `func run()error`, `serverless.Run(ctx,app);err!=nil&&ctx.Err()==nil{return err}`, `server.ListenAndServe();err!=nil&&err!=http.ErrServerClosed{return err}`} {
			if !strings.Contains(source, part) {
				t.Fatal("missing generated contract", part)
			}
		}
		if strings.Contains(source, "NewZones(ctx,zones);if err!=nil{log.Fatal") {
			t.Fatal("startup error bypasses cleanup")
		}
		if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
			t.Fatal(err)
		}
	}
}

func TestZonesRejectPrerenderBeforeGeneratingOrExecuting(t *testing.T) {
	root := fixture(t, map[string]string{"site/app/page.go": "package app", "site/app/prerender.go": "package app\nfunc Prerender(){panic(\"must never execute\")}"})
	writeInput(t, root, "zones.json", `{"version":1,"zones":[{"id":"site","host":"example.test","app":"site"}]}`)
	var output bytes.Buffer
	if _, err := CompileZones(context.Background(), filepath.Join(root, "zones.json"), false, &output); err == nil || !strings.Contains(err.Error(), "no capture or seed reservation") {
		t.Fatal(err, output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "site", "routerefs")); !os.IsNotExist(err) {
		t.Fatal("generated before rejection", err)
	}
	if _, err := compile(context.Background(), filepath.Join(root, "site"), false, &output, false); err == nil || !strings.Contains(err.Error(), "no capture or seed reservation") {
		t.Fatal("rediscovery admitted prerender", err)
	}
}

func TestZonesRejectDifferentOwningModules(t *testing.T) {
	t.Setenv("GOWORK", "off")
	var projects []*Project
	for _, name := range []string{"first", "second"} {
		root := fixture(t, map[string]string{"app/page.go": "package app"})
		writeInput(t, root, "go.mod", "module fixture.test/"+name+"\n\ngo 1.24\n")
		p, err := Discover(root)
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, p)
	}
	if _, err := zonesBuildContext(context.Background(), projects); err == nil || !strings.Contains(err.Error(), "one owning Go module") {
		t.Fatal(err)
	}
}

// This isolated fixture exercises actual ordinary app compilation and the final
// aggregate binary. It never builds repository example applications.
func TestCompileZonesPortableBinary(t *testing.T) {
	t.Setenv("GOWORK", "off")
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeInput(t, root, "go.mod", fmt.Sprintf("module fixture.test/zones\n\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(frameworkRoot)))
	page := `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(c *nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("MARKER:"+nextgo.CurrentZone(c.Context).ID)},nil}
`
	config := `//go:build !js
package app
import("os";"net/http";nextgo "github.com/Suhaibinator/Next.go")
func Configure(c *nextgo.Config){if c.Zone().ID==""{panic("missing trusted zone")};c.Headers=http.Header{"X-Zone":{c.Zone().ID},"X-Phase":{string(c.Phase)}};if name:=os.Getenv("ZONE_TEST_CONFIG_RECORD");name!=""{f,err:=os.OpenFile(name,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{panic(err)};defer f.Close();f.WriteString(c.Zone().ID+"\n")};if os.Getenv("ZONE_TEST_PRERENDER")=="true"{c.Routes[0].Prerender=&nextgo.PrerenderPolicy{}};switch os.Getenv("ZONE_TEST_PURPOSE"){case "phase":c.Phase=nextgo.PhaseStaticExport;case "dev":c.Dev=!c.Dev;case "prerender":c.PrerenderBuildMode=true}}
`
	for _, name := range []string{"marketing", "blog"} {
		writeInput(t, root, name+"/app/page.go", strings.Replace(page, "MARKER", name, 1))
		writeInput(t, root, name+"/app/config.go", config)
		writeInput(t, root, name+"/public/style.css", name+" css")
		writeInput(t, root, name+"/app/robots.txt", name+" robots")
	}
	writeInput(t, root, "zones.json", `{"version":1,"zones":[{"id":"marketing","host":"site.test","app":"marketing"},{"id":"blog","host":"site.test","basePath":"/blog","app":"blog"},{"id":"tenant","tenant":"alpha","host":"alpha.test","app":"marketing"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	var logs bytes.Buffer
	build, err := CompileZones(ctx, filepath.Join(root, "zones.json"), false, &logs)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer os.RemoveAll(build.Dir)
	if build.Project != nil || len(build.ZoneProjects) != 2 || len(build.ZoneAssets) != 2 || build.ZoneRuntimeIDs["marketing"] != build.ZoneRuntimeIDs["tenant"] || build.ZoneRuntimeIDs["blog"] == build.ZoneRuntimeIDs["tenant"] {
		t.Fatal("bad aggregate metadata", build)
	}
	current, err := ZonesFingerprint(ctx, filepath.Join(root, "zones.json"))
	if err != nil || current != build.Fingerprint {
		t.Fatal("source snapshot drift", err)
	}
	for _, zone := range []string{"marketing", "blog", "tenant"} {
		if _, err := os.Stat(filepath.Join(build.Dir, "server", "private", zone, "prerender.json")); err != nil {
			t.Fatal(err)
		}
	}
	record := filepath.Join(root, "configured")
	env := append(os.Environ(), "ZONE_TEST_CONFIG_RECORD="+record, "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_ENV_DIR=", "NEXTGO_ZONE_HOSTS=", "ZONE_TEST_PRERENDER=", "ZONE_TEST_PURPOSE=")
	probe := exec.CommandContext(ctx, build.Binary, "-static-paths")
	probe.Dir = t.TempDir()
	probe.Env = env
	out, err := probe.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unsupported") && !strings.Contains(string(out), "does not support aggregate") {
		t.Fatal("accepted aggregate purpose", err, string(out))
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("unsupported purpose ran hooks", err)
	}
	probe = exec.CommandContext(ctx, build.Binary)
	probe.Env = append(env, "ZONE_TEST_PRERENDER=true")
	out, err = probe.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "configured prerender policies") {
		t.Fatal("accepted configured prerender", err, string(out))
	}
	for _, purpose := range []string{"phase", "dev", "prerender"} {
		probe = exec.CommandContext(ctx, build.Binary)
		probe.Env = append(env, "ZONE_TEST_PURPOSE="+purpose)
		out, err = probe.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Configure must preserve native startup purpose") {
			t.Fatal("accepted configured purpose override", purpose, err, string(out))
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	cmd := exec.CommandContext(ctx, build.Binary, "-addr", address)
	cmd.Dir = t.TempDir()
	cmd.Env = env
	var serverLog bytes.Buffer
	cmd.Stdout = &serverLog
	cmd.Stderr = &serverLog
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	client := &http.Client{Timeout: time.Second}
	request := func(host, path string) (int, string, http.Header, error) {
		req, _ := http.NewRequest("GET", "http://"+address+path, nil)
		req.Host = host
		res, err := client.Do(req)
		if err != nil {
			return 0, "", nil, err
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		return res.StatusCode, string(data), res.Header, err
	}
	ready := time.Now().Add(8 * time.Second)
	for {
		status, _, _, err := request("site.test", "/")
		if err == nil && status == 200 {
			break
		}
		if time.Now().After(ready) {
			cmd.Process.Kill()
			cmd.Wait()
			stopped = true
			t.Fatal("aggregate not ready", err, serverLog.String())
		}
		time.Sleep(15 * time.Millisecond)
	}
	for _, test := range []struct{ host, path, body, zone string }{{"site.test", "/", "marketing:marketing", "marketing"}, {"site.test", "/blog", "blog:blog", "blog"}, {"alpha.test", "/", "marketing:tenant", "tenant"}, {"site.test", "/assets/style.css", "marketing css", ""}, {"site.test", "/blog/assets/style.css", "blog css", ""}, {"site.test", "/robots.txt", "marketing robots", ""}, {"site.test", "/blog/robots.txt", "blog robots", ""}} {
		status, body, headers, err := request(test.host, test.path)
		if err != nil || status != 200 || !strings.Contains(body, test.body) {
			t.Fatal(test, status, body, err)
		}
		if test.zone != "" && (headers.Get("X-Zone") != test.zone || headers.Get("X-Phase") != "production") {
			t.Fatal("Configure ran without phase/zone", headers)
		}
	}
	for _, test := range []struct {
		host, path string
		status     int
	}{{"unknown.test", "/", 421}, {"site.test", "/blog/_nextgo/" + build.ZoneRuntimeIDs["marketing"] + "/boot.js", 404}, {"alpha.test", "/blog/robots.txt", 404}} {
		status, _, _, err := request(test.host, test.path)
		if err != nil || status != test.status {
			t.Fatal("zone ownership leak", test, status, err)
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, serverLog.String())
	}
	stopped = true
}
