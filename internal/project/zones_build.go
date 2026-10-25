package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxZonesArtifactBytes = 128 << 20
const maxZonesArtifactEntries = 16384

// zonesBuildContext deliberately supports one owning module in v1. Identical
// effective settings matter: the final native entry imports every app in one Go
// invocation, so independently valid but different workspaces/flags cannot merge.
func zonesBuildContext(ctx context.Context, projects []*Project) (string, error) {
	var first string
	for _, p := range projects {
		module, err := applicationModule(ctx, p.Root)
		if err != nil {
			return "", err
		}
		data, err := goInputJSON(ctx, p.Root, "env", "-json", "GOMOD", "GOWORK", "GOVERSION", "GOROOT", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT", "GOTOOLCHAIN")
		if err != nil {
			return "", err
		}
		var environment map[string]string
		if err := json.Unmarshal(data, &environment); err != nil {
			return "", err
		}
		for _, key := range []string{"GOMOD", "GOWORK", "GOROOT"} {
			if value := environment[key]; value != "" && value != "off" {
				environment[key] = canonicalInput(value)
			}
		}
		overlay, err := readCallerOverlay(ctx, p.Root)
		if err != nil {
			return "", err
		}
		encoded, _ := json.Marshal(struct {
			Dir, Path, GoMod string
			Environment      map[string]string
			Overlay          map[string]string
			Sources          map[string]string
		}{canonicalInput(module.Dir), module.Path, canonicalInput(module.GoMod), environment, overlay.Replace, overlay.Sources})
		value := string(encoded)
		if first == "" {
			first = value
		} else if value != first {
			return "", fmt.Errorf("zones v1 requires one owning Go module and identical effective workspace, SDK and build flags")
		}
	}
	sum := sha256.Sum256([]byte(first))
	return hex.EncodeToString(sum[:]), nil
}

type zonesArtifactBudget struct {
	bytes   int64
	entries int
}
type zonesCopyWriter struct {
	ctx    context.Context
	target io.Writer
	budget *zonesArtifactBudget
}

func (w zonesCopyWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.budget.bytes+int64(len(data)) > maxZonesArtifactBytes {
		return 0, zonesArtifactLimit()
	}
	n, err := w.target.Write(data)
	w.budget.bytes += int64(n)
	return n, err
}
func zonesArtifactLimit() error {
	return fmt.Errorf("zones artifact exceeds 128 MiB or 16384 entries; each zone embeds independent browser assets")
}
func copyZoneArtifact(ctx context.Context, source, target string, budget *zonesArtifactBudget) error {
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		budget.entries++
		if budget.entries > maxZonesArtifactEntries {
			return zonesArtifactLimit()
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("zone artifacts cannot contain symlinks")
		}
		destination := filepath.Join(target, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("zone artifact must be a regular file")
		}
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		defer input.Close()
		opened, err := input.Stat()
		if err != nil {
			return err
		}
		if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return fmt.Errorf("zone artifact changed while opening")
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(zonesCopyWriter{ctx, output, budget}, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return fmt.Errorf("zone artifact changed size while copying")
		}
		return nil
	})
}

type compiledZone struct {
	declaration ZoneManifest
	project     *Project
	runtimeID   string
}

// CompileZones produces one portable native handler with isolated per-zone
// browser/public/private trees. Prerender declarations and aggregate publication
// probes are explicitly unsupported in this first compiler slice.
func CompileZones(ctx context.Context, name string, dev bool, output io.Writer) (_ *Build, err error) {
	manifest, err := DiscoverZones(name)
	if err != nil {
		return nil, err
	}
	projects := map[string]*Project{}
	var unique []*Project
	for _, zone := range manifest.Zones {
		if projects[zone.Root] != nil {
			continue
		}
		p, err := Discover(zone.Root)
		if err != nil {
			return nil, fmt.Errorf("zone %q: %w", zone.ID, err)
		}
		if hasPrerenderFiles(p) {
			return nil, fmt.Errorf("zone %q: zones v1 does not support prerender declarations; no capture or seed reservation was executed", zone.ID)
		}
		projects[zone.Root] = p
		unique = append(unique, p)
	}
	settings, err := zonesBuildContext(ctx, unique)
	if err != nil {
		return nil, err
	}
	// Every app's deterministic public Go helpers must exist before any full
	// input snapshot. Otherwise generating a later app invalidates earlier ones.
	for _, p := range unique {
		if _, err := GenerateRoutes(p, false); err != nil {
			return nil, err
		}
		if _, err := GenerateActions(p, false); err != nil {
			return nil, err
		}
	}
	fingerprint, err := ZonesFingerprint(ctx, manifest.Path)
	if err != nil {
		return nil, err
	}
	generationRoot := filepath.Join(manifest.Root, ".nextgo")
	if err := os.MkdirAll(generationRoot, 0755); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(generationRoot, "build-zones-")
	if err != nil {
		return nil, err
	}
	success := false
	var ordinary []*Build
	defer func() {
		for _, build := range ordinary {
			_ = os.RemoveAll(build.Dir)
		}
		if !success {
			_ = os.RemoveAll(directory)
		}
	}()
	serverDir := filepath.Join(directory, "server")
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return nil, err
	}
	cache := map[string]*Build{}
	budget := &zonesArtifactBudget{}
	var zones []compiledZone
	assets := map[string]string{}
	runtimeIDs := map[string]string{}
	for _, declaration := range manifest.Zones {
		build := cache[declaration.Root]
		if build == nil {
			build, err = compile(ctx, declaration.Root, dev, output, false)
			if err != nil {
				return nil, fmt.Errorf("zone %q: %w", declaration.ID, err)
			}
			ordinary = append(ordinary, build)
			cache[declaration.Root] = build
			if hasPrerenderFiles(build.Project) {
				return nil, fmt.Errorf("zone sources changed to unsupported prerender declarations")
			}
		}
		assetDir := filepath.Join(serverDir, "assets", declaration.ID)
		if err := copyZoneArtifact(ctx, filepath.Join(build.Dir, "server", "assets"), assetDir, budget); err != nil {
			return nil, err
		}
		if err := copyZoneArtifact(ctx, filepath.Join(build.Dir, "server", "private"), filepath.Join(serverDir, "private", declaration.ID), budget); err != nil {
			return nil, err
		}
		assets[build.ID] = assetDir
		runtimeIDs[declaration.ID] = build.ID
		zones = append(zones, compiledZone{declaration, build.Project, build.ID})
	}
	unique = nil
	seen := map[string]bool{}
	for _, zone := range zones {
		if !seen[zone.project.Root] {
			seen[zone.project.Root] = true
			unique = append(unique, zone.project)
		}
	}
	finalSettings, err := zonesBuildContext(ctx, unique)
	if err != nil {
		return nil, err
	}
	if finalSettings != settings {
		return nil, fmt.Errorf("zone build settings changed during compilation")
	}
	identity := sha256.New()
	fmt.Fprintf(identity, "zones-v1:%s:%d:", settings, len(manifest.raw))
	identity.Write(manifest.raw)
	for _, zone := range zones {
		fmt.Fprintf(identity, "%d:%s:%s", len(zone.declaration.ID), zone.declaration.ID, zone.runtimeID)
	}
	id := hex.EncodeToString(identity.Sum(nil))[:20]
	if err := writeGo(filepath.Join(serverDir, "main.go"), zonesServerSource(zones, dev)); err != nil {
		return nil, err
	}
	target, overlay, err := generatedEntryOverlay(ctx, unique[0].Root, serverDir, "server")
	if err != nil {
		return nil, err
	}
	binary := filepath.Join(directory, "server-bin")
	if err := run(ctx, unique[0].Root, output, nil, "build", "-overlay="+overlay, "-trimpath", "-ldflags=-s -w", "-o", binary, target); err != nil {
		return nil, err
	}
	finalFingerprint, err := ZonesFingerprint(ctx, manifest.Path)
	if err != nil {
		return nil, err
	}
	data, _, err := readZonesManifest(manifest.Path)
	if err != nil {
		return nil, err
	}
	if finalFingerprint != fingerprint || !bytes.Equal(data, manifest.raw) {
		return nil, fmt.Errorf("zone sources or manifest changed during build; retry after saving")
	}
	finalSettings, err = zonesBuildContext(ctx, unique)
	if err != nil {
		return nil, err
	}
	if finalSettings != settings {
		return nil, fmt.Errorf("zone build settings changed during compilation")
	}
	success = true
	return &Build{Dir: directory, Binary: binary, ID: id, Fingerprint: fingerprint, ZoneProjects: unique, ZoneAssets: assets, ZoneRuntimeIDs: runtimeIDs, Zones: manifest}, nil
}

func zonesServerSource(zones []compiledZone, dev bool) string {
	imports := map[string]bool{}
	for _, zone := range zones {
		for path := range serverImports(zone.project) {
			imports[path] = true
		}
	}
	keys := make([]string, 0, len(imports))
	for path := range imports {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	aliases := map[string]string{}
	var b strings.Builder
	b.WriteString("package main\nimport(\"embed\";\"fmt\";\"flag\";\"io/fs\";\"log\";\"net/http\";\"os\";\"os/signal\";\"syscall\";\"context\";\"time\"\nnextgo \"" + framework + "\"\n\"" + framework + "/serverless\"\n\"" + framework + "/environment\"\n")
	for i, path := range keys {
		alias := fmt.Sprintf("p%d", i)
		aliases[path] = alias
		fmt.Fprintf(&b, "%s %q\n", alias, path)
	}
	b.WriteString(")\n//go:embed all:assets all:private\nvar embedded embed.FS\n")
	for i, zone := range zones {
		fmt.Fprintf(&b, "func zoneConfig%d(phase nextgo.Phase,generation string)nextgo.Config{assets,err:=fs.Sub(embedded,%q);if err!=nil{log.Fatal(err)}\n", i, "assets/"+zone.declaration.ID)
		writeConfigConstruction(&b, zone.project, aliases, zone.runtimeID, dev)
		fmt.Fprintf(&b, "config.Phase=phase;config.BasePath=%q;config.DevGeneration=generation;config.PrerenderFS,err=fs.Sub(embedded,%q);if err!=nil{log.Fatal(err)}\n", zone.declaration.BasePath, "private/"+zone.declaration.ID)
		writeMetadataSources(&b, zone.project, aliases, "assets/"+zone.declaration.ID+"/metadata")
		b.WriteString("return config}\n")
	}
	b.WriteString(`func main(){if err:=run();err!=nil{log.Print(err);os.Exit(1)}}
func run()error{
addr:=flag.String("addr",":3000","listen address");envDir:=flag.String("env-dir",os.Getenv("NEXTGO_ENV_DIR"),"explicit aggregate environment directory");envMode:=flag.String("env-mode","production","environment mode");generation:=flag.String("dev-generation","","development reload token");hosts:=flag.String("zone-hosts","","startup JSON zone ID to exact Host overrides; defaults to NEXTGO_ZONE_HOSTS")
prerender:=flag.String("prerender-out","","unsupported aggregate purpose");staticPaths:=flag.Bool("static-paths",false,"unsupported aggregate purpose");pathConfig:=flag.Bool("path-config",false,"unsupported aggregate purpose");staticExport:=flag.Bool("static-export",false,"unsupported aggregate purpose");exportCheck:=flag.Bool("export-check",false,"unsupported aggregate purpose");exportConfig:=flag.Bool("export-config",false,"unsupported aggregate purpose");exportDomain:=flag.String("export-domain","","unsupported aggregate purpose");flag.Parse()
if *prerender!=""||*staticPaths||*pathConfig||*staticExport||*exportCheck||*exportConfig||*exportDomain!=""{log.Fatal("zones v1 does not support aggregate export, static enumeration, or prerender purposes")}
if *envDir!=""{if err:=environment.Apply(*envDir,environment.Mode(*envMode));err!=nil{log.Fatal(err)}}
specified:=false;flag.Visit(func(f *flag.Flag){if f.Name=="zone-hosts"{specified=true}});if !specified{*hosts=os.Getenv("NEXTGO_ZONE_HOSTS")}
phase:=nextgo.PhaseProduction
`)
	if dev {
		b.WriteString("phase=nextgo.PhaseDevelopment\n")
	}
	b.WriteString("known:=[]string{")
	for _, zone := range zones {
		fmt.Fprintf(&b, "%q,", zone.declaration.ID)
	}
	b.WriteString("}\noverrides,err:=nextgo.ParseZoneHosts(*hosts,known);if err!=nil{log.Fatal(err)}\nzones:=[]nextgo.ZoneConfig{\n")
	for i, zone := range zones {
		fmt.Fprintf(&b, "{ID:%q,Host:%q,Tenant:%q,Config:zoneConfig%d(phase,*generation),Configure:func(config *nextgo.Config)error{\n", zone.declaration.ID, zone.declaration.Host, zone.declaration.Tenant, i)
		if zone.project.Configure != "" {
			fmt.Fprintf(&b, "%s.Configure(config)\n", aliases[zone.project.Configure])
		}
		if len(zone.project.Actions) > 0 {
			fmt.Fprintf(&b, "config.ActionRegistrars=append(config.ActionRegistrars,%s.Register)\n", aliases[zone.project.Import+"/actionrefs"])
		}
		fmt.Fprintf(&b, "if config.Phase!=phase||config.Dev!=%t||config.PrerenderBuildMode{return fmt.Errorf(\"zones v1 Configure must preserve native startup purpose\")}\n", dev)
		b.WriteString("for _,route:=range config.Routes{if route.Prerender!=nil{return fmt.Errorf(\"zones v1 does not support configured prerender policies\")}}\nreturn nil}},\n")
	}
	b.WriteString(`}
for i:=range zones{if host,ok:=overrides[zones[i].ID];ok{zones[i].Host=host}}
ctx,stop:=signal.NotifyContext(context.Background(),syscall.SIGINT,syscall.SIGTERM);defer stop()
app,err:=nextgo.NewZones(ctx,zones);if err!=nil{return err}
defer func(){drain,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();if err:=app.Close(drain);err!=nil{log.Printf("Next.go zones drain: %v",err)}}()
if os.Getenv("AWS_LAMBDA_RUNTIME_API")!=""{if err:=serverless.Run(ctx,app);err!=nil&&ctx.Err()==nil{return err};return nil}
server:=&http.Server{Addr:*addr,Handler:app,ReadHeaderTimeout:10*time.Second};done:=make(chan struct{})
go func(){defer close(done);<-ctx.Done();drain,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();if err:=server.Shutdown(drain);err!=nil{_=server.Close()}}()
defer func(){stop();<-done}()
log.Printf("Next.go zones listening on %s",*addr)
if err:=server.ListenAndServe();err!=nil&&err!=http.ErrServerClosed{return err}
<-done
return nil
}
`)
	return b.String()
}
