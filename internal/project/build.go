package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/Suhaibinator/Next.go/environment"
	"github.com/Suhaibinator/Next.go/internal/prerenderid"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const framework = "github.com/Suhaibinator/Next.go"

type Build struct {
	Dir, Binary, ID, Fingerprint string
	Project                      *Project // Exact discovery metadata used by this successful build.
}

func Compile(ctx context.Context, root string, dev bool, output io.Writer) (*Build, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	inputFingerprint, err := sourceFingerprintContext(ctx, absRoot)
	if err != nil {
		return nil, err
	}
	p, err := Discover(root)
	if err != nil {
		return nil, err
	}
	coreTags, moduleTags, err := browserBuildTags(ctx, p.Root)
	if err != nil {
		return nil, err
	}
	if _, err = GenerateRoutes(p, false); err != nil {
		return nil, err
	}
	if _, err = GenerateActions(p, false); err != nil {
		return nil, err
	}
	fingerprint, err := fingerprintExceptContext(ctx, p.Root, "")
	if err != nil {
		return nil, err
	}
	generations := filepath.Join(p.Root, ".nextgo")
	if err = os.MkdirAll(generations, 0755); err != nil {
		return nil, err
	}
	buildDir, err := os.MkdirTemp(generations, "build-")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(buildDir)
		}
	}()
	generatedAssets, err := stageAssetPipeline(ctx, p.Root, buildDir, output)
	if err != nil {
		return nil, err
	}
	if hasStaticMetadata(p) {
		if err = CopyMetadataFiles(ctx, p, filepath.Join(buildDir, "server", "assets", "metadata")); err != nil {
			return nil, err
		}
	}
	modules, err := compileLazy(ctx, p, buildDir, fingerprint, moduleTags, output)
	if err != nil {
		return nil, err
	}
	clientDir := filepath.Join(buildDir, "client")
	if err = os.MkdirAll(clientDir, 0755); err != nil {
		return nil, err
	}
	if err = writeGo(filepath.Join(clientDir, "main.go"), clientSource(p, modules)); err != nil {
		return nil, err
	}
	if err := validateBrowserGraph(ctx, p, clientDir, coreTags); err != nil {
		return nil, err
	}
	// Stable virtual packages retain module/VCS provenance without leaking the
	// private generation directory into compiler identity or source filenames.
	clientTarget, clientOverlay, err := generatedEntryOverlay(ctx, p.Root, clientDir, "client")
	if err != nil {
		return nil, err
	}
	wasm := filepath.Join(buildDir, "app.wasm")
	if err = run(ctx, p.Root, output, []string{"GOOS=js", "GOARCH=wasm"}, "build", coreTags, "-overlay="+clientOverlay, "-trimpath", "-ldflags=-s -w", "-o", wasm, clientTarget); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(wasm)
	if err != nil {
		return nil, err
	}
	support, err := wasmSupport(ctx, p.Root)
	if err != nil {
		return nil, err
	}
	// All bytes published under immutable runtime URLs participate in the ID.
	id := runtimeBuildID(data, p.Import+"\x00"+fingerprint+"\x00"+lazyIdentity(modules)+generatedAssetIdentity(generatedAssets), support, bootstrap)
	serverDir := filepath.Join(buildDir, "server")
	assets := filepath.Join(serverDir, "assets")
	runtimeDir := filepath.Join(assets, "_nextgo", id)
	if err = os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, err
	}
	for _, module := range modules {
		if err := os.WriteFile(filepath.Join(runtimeDir, module.File), module.Data, 0644); err != nil {
			return nil, err
		}
	}
	if err = os.Rename(wasm, filepath.Join(runtimeDir, "app.wasm")); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(runtimeDir, "wasm_exec.js"), support, 0644); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(runtimeDir, "boot.js"), []byte(bootstrap), 0644); err != nil {
		return nil, err
	}
	public := filepath.Join(assets, "public")
	if err = os.MkdirAll(public, 0755); err != nil {
		return nil, err
	}
	if _, err = os.Lstat(filepath.Join(p.Root, "public")); err == nil {
		if err = copyTree(filepath.Join(p.Root, "public"), public); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err = mergeGeneratedAssets(ctx, generatedAssets, public); err != nil {
		return nil, err
	}
	private := filepath.Join(serverDir, "private")
	if err = os.MkdirAll(private, 0755); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(private, "prerender.json"), []byte("{}"), 0600); err != nil {
		return nil, err
	}
	if err = writeGo(filepath.Join(serverDir, "main.go"), serverSource(p, id, dev)); err != nil {
		return nil, err
	}
	binary := filepath.Join(buildDir, "server-bin")
	if !dev && hasPrerenderFiles(p) {
		// Execute build-time application code on the host, even for a Linux target.
		probe := filepath.Join(buildDir, "prerender-probe")
		serverTarget, serverOverlay, overlayErr := generatedEntryOverlay(ctx, p.Root, serverDir, "server")
		if overlayErr != nil {
			return nil, overlayErr
		}
		if err = run(ctx, p.Root, output, []string{"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH}, "build", "-overlay="+serverOverlay, "-trimpath", "-ldflags=-s -w", "-o", probe, serverTarget); err != nil {
			return nil, err
		}
		artifact := filepath.Join(private, "prerender.json")
		buildEnv, err := environment.Load(p.Root, environment.Production, append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_ENV_DIR="))
		if err != nil {
			return nil, err
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		cmd := exec.CommandContext(probeCtx, probe, "-prerender-out", artifact)
		cmd.Dir = p.Root
		cmd.Env = buildEnv
		cmd.Stdout = output
		cmd.Stderr = output
		err = cmd.Run()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("prerender build: %w", err)
		}
		info, err := os.Stat(artifact)
		if err != nil {
			return nil, err
		}
		if info.Size() > 32<<20 {
			return nil, fmt.Errorf("prerender artifact exceeds 32 MiB")
		}
		snapshot, err := os.ReadFile(artifact)
		if err != nil {
			return nil, err
		}
		finalID := prerenderid.Build(id, snapshot)
		if err = os.Rename(runtimeDir, filepath.Join(assets, "_nextgo", finalID)); err != nil {
			return nil, err
		}
		id = finalID
		if err = writeGo(filepath.Join(serverDir, "main.go"), serverSource(p, id, false)); err != nil {
			return nil, err
		}
		if err = os.Remove(probe); err != nil {
			return nil, err
		}
	}
	serverTarget, serverOverlay, err := generatedEntryOverlay(ctx, p.Root, serverDir, "server")
	if err != nil {
		return nil, err
	}
	if err = run(ctx, p.Root, output, nil, "build", "-overlay="+serverOverlay, "-trimpath", "-ldflags=-s -w", "-o", binary, serverTarget); err != nil {
		return nil, err
	}
	if err := validateGeneratedVirtualRoot(p.Root); err != nil {
		return nil, err
	}
	finalFingerprint, err := fingerprintExceptContext(ctx, p.Root, "")
	if err != nil {
		return nil, err
	}
	if finalFingerprint != fingerprint {
		return nil, fmt.Errorf("sources changed during build; retrying on the next development cycle (or rerun build)")
	}
	finalInputFingerprint, err := sourceFingerprintContext(ctx, p.Root)
	if err != nil {
		return nil, err
	}
	if finalInputFingerprint != inputFingerprint {
		return nil, fmt.Errorf("sources changed during discovery or generation; retrying on the next development cycle (or rerun build)")
	}
	success = true
	return &Build{Dir: buildDir, Binary: binary, ID: id, Fingerprint: fingerprint, Project: p}, nil
}

func runtimeBuildID(wasm []byte, fingerprint string, support []byte, loader string) string {
	hash := sha256.New()
	for _, part := range [][]byte{wasm, []byte(fingerprint), support, []byte(loader)} {
		fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))[:20]
}
func wasmSupport(ctx context.Context, appRoot string) ([]byte, error) {
	// Toolchain auto-selection depends on the application's go.mod/go.work.
	// Resolve GOROOT in the same directory used by its WASM compiler invocation.
	command := exec.CommandContext(ctx, "go", "env", "GOROOT")
	command.Dir = appRoot
	command.Env = command.Environ()
	root, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("resolve application Go runtime: %w", err)
	}
	return os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "lib", "wasm", "wasm_exec.js"))
}

func serverSource(p *Project, id string, dev bool) string {
	imports := map[string]bool{}
	for _, r := range projectRoutes(p.Routes) {
		for _, slot := range r.Slots {
			if slot.Default != "" {
				imports[slot.Default] = true
			}
		}
		imports[r.Import] = true
		for _, layer := range r.StaticParamsLayers {
			imports[layer.Import] = true
		}
		for _, l := range r.Layouts {
			imports[l] = true
		}
		for _, layer := range r.Metadata {
			imports[layer.Import] = true
		}
		for _, boundary := range r.Access {
			for _, handler := range []string{boundary.Unauthorized, boundary.Forbidden} {
				if handler != "" {
					imports[handler] = true
				}
			}
		}
		for _, handler := range []string{r.NotFound, r.Error, r.Loading} {
			if handler != "" {
				imports[handler] = true
			}
		}
	}
	if p.NotFound != "" {
		imports[p.NotFound] = true
	}
	if p.Configure != "" {
		imports[p.Configure] = true
	}
	for _, metadata := range p.MetadataFiles {
		if !metadata.Static() {
			imports[metadata.Import] = true
		}
	}
	if len(p.Actions) > 0 {
		imports[p.Import+"/actionrefs"] = true
	}
	for _, handler := range []string{p.Unauthorized, p.Forbidden} {
		if handler != "" {
			imports[handler] = true
		}
	}
	keys := make([]string, 0, len(imports))
	for key := range imports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	aliases := map[string]string{}
	var b strings.Builder
	b.WriteString(`package main
import("embed";"flag";"io/fs";"log";"net/http";"os";"os/signal";"syscall";"context";"time";"encoding/json"
nextgo "` + framework + `"
"` + framework + `/serverless"
"` + framework + `/environment"
`)
	for i, key := range keys {
		alias := fmt.Sprintf("p%d", i)
		aliases[key] = alias
		fmt.Fprintf(&b, "%s %q\n", alias, key)
	}
	b.WriteString(")\n//go:embed all:assets all:private\nvar embedded embed.FS\nfunc main(){\nprerenderOut:=flag.String(\"prerender-out\",\"\",\"write private build snapshots and exit\");addr:=flag.String(\"addr\",\":3000\",\"listen address\");staticPaths:=flag.Bool(\"static-paths\",false,\"write static route paths as JSON and exit\");pathConfig:=flag.Bool(\"path-config\",false,\"write deployment paths as JSON and exit\");staticExport:=flag.Bool(\"static-export\",false,\"serve for static publication\");exportCheck:=flag.Bool(\"export-check\",false,\"validate static export support and exit\");exportConfig:=flag.Bool(\"export-config\",false,\"write static publication configuration as JSON and exit\");exportDomain:=flag.String(\"export-domain\",\"\",\"select a configured locale host for static export\");envDir:=flag.String(\"env-dir\",os.Getenv(\"NEXTGO_ENV_DIR\"),\"explicit application environment directory\");envMode:=flag.String(\"env-mode\",\"production\",\"environment mode\");devGeneration:=flag.String(\"dev-generation\",\"\",\"development reload token\");flag.Parse();exporting:=*staticExport||*staticPaths||*exportCheck||*exportConfig;if *prerenderOut!=\"\"&&exporting{log.Fatal(\"prerender and static export purposes conflict\")};if *envDir!=\"\" {if err:=environment.Apply(*envDir,environment.Mode(*envMode));err!=nil{log.Fatal(err)}}\nassets,err:=fs.Sub(embedded,\"assets\");if err!=nil{log.Fatal(err)}\n")
	fmt.Fprintf(&b, "config:=nextgo.Config{Assets:assets,BuildID:%q,Dev:%t,", id, dev)
	if p.NotFound != "" {
		fmt.Fprintf(&b, "NotFound:%s.NotFound,", aliases[p.NotFound])
	}
	for _, handler := range []struct{ field, imp string }{{"Unauthorized", p.Unauthorized}, {"Forbidden", p.Forbidden}} {
		if handler.imp != "" {
			fmt.Fprintf(&b, "%s:%s.%s,", handler.field, aliases[handler.imp], handler.field)
		}
	}
	b.WriteString("Routes:[]nextgo.Route{\n")
	for _, r := range p.Routes {
		writeRoute(&b, r, aliases, p.NotFound)
	}
	b.WriteString("}}\nconfig.Phase=nextgo.PhaseProduction\nif config.Dev{config.Phase=nextgo.PhaseDevelopment}\nif exporting{config.Phase=nextgo.PhaseStaticExport}\nif *prerenderOut!=\"\"{config.Phase=nextgo.PhasePrerender}\nconfig.DevGeneration=*devGeneration\nconfig.PrerenderBuildMode=*prerenderOut!=\"\"\nconfig.PrerenderFS,err=fs.Sub(embedded,\"private\");if err!=nil{log.Fatal(err)}\n")
	writeMetadataSources(&b, p, aliases)
	if p.Configure != "" {
		fmt.Fprintf(&b, "%s.Configure(&config)\n", aliases[p.Configure])
	}
	if len(p.Actions) > 0 {
		fmt.Fprintf(&b, "config.ActionRegistrars=append(config.ActionRegistrars,%s.Register)\n", aliases[p.Import+"/actionrefs"])
	}
	b.WriteString(`ctx,stop:=signal.NotifyContext(context.Background(),syscall.SIGINT,syscall.SIGTERM);defer stop()
app,err:=nextgo.NewContext(ctx,config);if err!=nil{log.Fatal(err)};if *exportCheck{if _,err=app.StaticExportConfig(nextgo.StaticExportOptions{Domain:*exportDomain});err!=nil{log.Fatal(err)};return}
if *exportConfig{info,err:=app.StaticExportConfig(nextgo.StaticExportOptions{Domain:*exportDomain});if err!=nil{log.Fatal(err)};if err=json.NewEncoder(os.Stdout).Encode(info);err!=nil{log.Fatal(err)};return}
if *prerenderOut!=""{buildCtx,cancel:=context.WithTimeout(ctx,90*time.Second);defer cancel();data,err:=app.BuildPrerender(buildCtx);if err!=nil{log.Fatal(err)};if err=os.WriteFile(*prerenderOut,data,0600);err!=nil{log.Fatal(err)};return}
if *pathConfig{if err:=json.NewEncoder(os.Stdout).Encode(app.Paths());err!=nil{log.Fatal(err)};return}
if *staticPaths{enumeration,cancel:=context.WithTimeout(ctx,30*time.Second);defer cancel();paths,err:=app.StaticPathsForDomain(enumeration,*exportDomain);if err!=nil{log.Fatal(err)};encoder:=json.NewEncoder(os.Stdout);encoder.SetEscapeHTML(false);if err=encoder.Encode(paths);err!=nil{log.Fatal(err)};return}
if os.Getenv("AWS_LAMBDA_RUNTIME_API")!=""{
defer func(){drain,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();if err:=app.Close(drain);err!=nil{log.Printf("Next.go completion drain: %v",err)}}()
if err:=serverless.Run(ctx,app);err!=nil && ctx.Err()==nil{log.Fatal(err)};return}
server:=&http.Server{Addr:*addr,Handler:app,ReadHeaderTimeout:10*time.Second}
shutdownDone:=make(chan struct{})
go func(){<-ctx.Done();shutdown,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();_ = server.Shutdown(shutdown);if err:=app.Close(shutdown);err!=nil{log.Printf("Next.go completion drain: %v",err)};close(shutdownDone)}()
log.Printf("Next.go listening on %s",*addr)
if err:=server.ListenAndServe();err!=nil && err!=http.ErrServerClosed{log.Fatal(err)}
<-shutdownDone
}
`)
	return b.String()
}

func writeGo(path, source string) error {
	data, err := format.Source([]byte(source))
	if err != nil {
		return fmt.Errorf("generate %s: %w", path, err)
	}
	return os.WriteFile(path, data, 0644)
}
func run(ctx context.Context, dir string, output io.Writer, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	// Environ updates PWD for Dir; preserving the caller's PWD selects the
	// wrong Go module when compiling an application outside this checkout.
	cmd.Env = cmd.Environ()
	for _, pair := range env {
		key := strings.SplitN(pair, "=", 2)[0] + "="
		filtered := cmd.Env[:0]
		for _, old := range cmd.Env {
			if !strings.HasPrefix(old, key) {
				filtered = append(filtered, old)
			}
		}
		cmd.Env = append(filtered, pair)
	}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name := strings.ToLower(d.Name()); name == ".env" || strings.HasPrefix(name, ".env.") {
			return fmt.Errorf("environment files cannot be public assets: %s", path)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("public assets cannot contain symlinks: %s", path)
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

// Fingerprint watches application and framework sources, including additions and
// deletions. Content hashing avoids missed edits on coarse timestamp filesystems.
func Fingerprint(root string) (string, error) {
	return fingerprintExcept(root, "")
}

func sourceFingerprint(root string) (string, error) {
	return sourceFingerprintContext(context.Background(), root)
}
func sourceFingerprintContext(ctx context.Context, root string) (string, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return fingerprintExceptContext(ctx, root, routesFile(canonical))
}

// Generated output participates in build IDs and development watches. The
// additional input-only snapshot lets Compile detect edits during discovery
// while allowing its own deterministic helper publication before compilation.
func fingerprintExcept(root, skip string) (string, error) {
	return fingerprintExceptContext(context.Background(), root, skip)
}
func fingerprintExceptContext(parent context.Context, root, skip string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	plan, err := resolveInputs(ctx, root)
	if err != nil {
		return "", err
	}
	return hashInputsContext(ctx, plan, skip)
}

const bootstrap = `(async () => {
  // Capture native restores even while the Go runtime is still loading.
  addEventListener('pagehide', event => {
    if (event.persisted && document.body?.hasAttribute('data-nextgo-draft')) {
      if (typeof globalThis.__nextgoFreeze === 'function') globalThis.__nextgoFreeze();
      document.body.setAttribute('data-nextgo-frozen', '');
    }
  });
  addEventListener('pageshow', event => {
    if (event.persisted && document.body?.hasAttribute('data-nextgo-draft')) {
      document.body.setAttribute('data-nextgo-frozen', '');
      if (typeof globalThis.__nextgoResume === 'function') globalThis.__nextgoResume();
      else location.reload();
    }
  });
  if (!document.getElementById('nextgo-initial-ready')) await new Promise(resolve => {
    const ready = () => {
      if (!document.getElementById('nextgo-initial-ready')) return;
      observer.disconnect();
      resolve();
    };
    const observer = new MutationObserver(ready);
    observer.observe(document.documentElement, {childList: true, subtree: true});
    ready();
  });
  const body = document.body;
  const base = body.dataset.nextgoRuntime || ('/_nextgo/' + body.dataset.nextgoBuild + '/');
  const status = document.getElementById('nextgo-status');
  const fail = error => { console.error(error); status.hidden = false; status.textContent = 'Interactive features could not start. Reload to retry.'; body.dataset.nextgoHydration = 'error'; };
  if (body.dataset.nextgoDev) {
    const events = new EventSource('/_nextgo/events?build=' + encodeURIComponent(body.dataset.nextgoBuild) + '&generation=' + encodeURIComponent(body.dataset.nextgoDevGeneration || ''));
    events.addEventListener('reload', () => location.reload());
    events.addEventListener('build-error', e => { status.hidden = false; status.textContent = 'Build failed: ' + JSON.parse(e.data); });
  }
  (async () => {
    const go = new Go();
    const response = await fetch(base + 'app.wasm');
    if (!response.ok) throw new Error('WASM request failed: ' + response.status);
    const bytes = await response.arrayBuffer();
    const result = await WebAssembly.instantiate(bytes, go.importObject);
    await go.run(result.instance);
  })().catch(fail);
})();
`

func writeRoute(b *strings.Builder, r Route, aliases map[string]string, rootNotFound string) {
	fmt.Fprintf(b, "{Pattern:%q,", r.Pattern)
	if r.API {
		fmt.Fprintf(b, "API:%s.Handle", aliases[r.Import])
	} else {
		switch {
		case r.Missing:
			b.WriteString("Page:func(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{},nextgo.ErrNotFound},")
		case r.Default:
			fmt.Fprintf(b, "Page:%s.Default,", aliases[r.Import])
		default:
			fmt.Fprintf(b, "Page:%s.Page,", aliases[r.Import])
		}
		if r.Primary != nil {
			fmt.Fprintf(b, "Primary:&nextgo.PrimarySlot{ID:%q,Base:%q,Layout:%d,Fallback:%t},", r.Primary.ID, r.Primary.Base, r.Primary.Layout, r.Primary.Fallback)
		}
		if len(r.Primaries) > 0 {
			b.WriteString("Primaries:[]nextgo.PrimarySlot{")
			for _, p := range r.Primaries {
				fmt.Fprintf(b, "{ID:%q,Base:%q,Layout:%d,Fallback:%t},", p.ID, p.Base, p.Layout, p.Fallback)
			}
			b.WriteString("},")
		}
		if r.Prerender {
			fmt.Fprintf(b, "Prerender:%s.Prerender(),", aliases[r.Import])
		}
		if r.Regions {
			fmt.Fprintf(b, "Regions:%s.Regions(),", aliases[r.Import])
		}
		if r.StaticParams {
			fmt.Fprintf(b, "StaticParams:%s.StaticParams,StaticParamsID:%q,", aliases[r.Import], r.Import)
		}
		b.WriteString("StaticParamsLayers:[]nextgo.StaticParamsLayer{")
		for _, layer := range r.StaticParamsLayers {
			fmt.Fprintf(b, "{ID:%q,Pattern:%q,Load:%s.StaticParams},", layer.Import, layer.Pattern, aliases[layer.Import])
		}
		b.WriteString("},")
		b.WriteString("Layouts:[]nextgo.LayoutFunc{")
		for i, l := range r.Layouts {
			if pattern, ok := r.Templates[i]; ok {
				fmt.Fprintf(b, "nextgo.Template(%q,%q,%s.Template),", l, pattern, aliases[l])
			} else {
				fmt.Fprintf(b, "%s.Layout,", aliases[l])
			}
		}
		b.WriteString("},LayoutBases:[]string{")
		for _, base := range r.LayoutBases {
			fmt.Fprintf(b, "%q,", base)
		}
		b.WriteString("},Metadata:[]nextgo.MetadataLayer{")
		for _, layer := range r.Metadata {
			fmt.Fprintf(b, "{Load:%s.Metadata,Layouts:%d},", aliases[layer.Import], layer.Layouts)
		}
		b.WriteString("},Access:[]nextgo.AccessBoundary{")
		for _, boundary := range r.Access {
			fmt.Fprintf(b, "{Layouts:%d,", boundary.Layouts)
			for _, handler := range []struct{ field, imp string }{{"Unauthorized", boundary.Unauthorized}, {"Forbidden", boundary.Forbidden}} {
				if handler.imp != "" {
					fmt.Fprintf(b, "%s:%s.%s,", handler.field, aliases[handler.imp], handler.field)
				}
			}
			b.WriteString("},")
		}
		b.WriteString("},LayoutIDs:[]string{")
		for _, layoutID := range r.LayoutIDs {
			fmt.Fprintf(b, "%q,", layoutID)
		}
		b.WriteString("}")
		for _, handler := range []struct{ field, imp string }{{"NotFound", r.NotFound}, {"Error", r.Error}, {"Loading", r.Loading}} {
			if handler.imp != "" && !(handler.field == "NotFound" && handler.imp == rootNotFound) {
				fmt.Fprintf(b, ",%s:%s.%s", handler.field, aliases[handler.imp], handler.field)
			}
		}
	}
	if len(r.Slots) > 0 {
		b.WriteString(",Slots:[]nextgo.Slot{")
		for _, slot := range r.Slots {
			fmt.Fprintf(b, "{ID:%q,Name:%q,Base:%q,Layout:%d,", slot.ID, slot.Name, slot.Base, slot.Layout)
			if slot.DefaultRoute != nil {
				b.WriteString("DefaultRoute:&nextgo.Route")
				writeRoute(b, *slot.DefaultRoute, aliases, "")
			} else if slot.Default != "" {
				fmt.Fprintf(b, "Default:%s.Default,", aliases[slot.Default])
			}
			b.WriteString("Routes:[]nextgo.Route{")
			for _, branch := range slot.Routes {
				writeRoute(b, branch, aliases, "")
			}
			b.WriteString("},Intercepts:[]nextgo.Route{")
			for _, branch := range slot.Intercepts {
				writeRoute(b, branch, aliases, "")
			}
			b.WriteString("}},")
		}
		b.WriteString("}")
	}
	b.WriteString("},\n")
}
func projectRoutes(routes []Route) []Route {
	result := []Route{}
	for _, route := range routes {
		result = append(result, route)
		for _, slot := range route.Slots {
			if slot.DefaultRoute != nil {
				result = append(result, projectRoutes([]Route{*slot.DefaultRoute})...)
			}
			result = append(result, projectRoutes(slot.Routes)...)
			result = append(result, projectRoutes(slot.Intercepts)...)
		}
	}
	return result
}

func clientSource(p *Project, modules []lazyOutput) string {
	var client strings.Builder
	client.WriteString("package main\nimport (\n\"" + framework + "/client\"\n")
	for i, c := range p.Components {
		if !c.Lazy {
			fmt.Fprintf(&client, "c%d %q\n", i, c.Import)
		}
	}
	client.WriteString(")\nfunc main(){\n")
	for i, c := range p.Components {
		if !c.Lazy {
			fmt.Fprintf(&client, "client.Register(%q,c%d.Mount)\n", c.Name, i)
		}
	}
	for _, module := range modules {
		fmt.Fprintf(&client, "client.RegisterLazy(%q,client.LazyModule{File:%q,Digest:%q,Epoch:%q,Size:%d})\n", module.Name, module.File, module.Digest, module.Epoch, module.Size)
	}
	client.WriteString("client.Run();select{}\n}\n")
	return client.String()
}

func hasPrerenderFiles(p *Project) bool {
	for _, route := range projectRoutes(p.Routes) {
		if route.Prerender {
			return true
		}
	}
	return false
}
