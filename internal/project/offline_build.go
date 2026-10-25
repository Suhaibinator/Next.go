package project

import (
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

	"github.com/Suhaibinator/Next.go/buildassets"
	"github.com/Suhaibinator/Next.go/offline"
	"github.com/Suhaibinator/Next.go/offline/worker"
)

const offlineShimPlaceholder = "__NEXTGO_OFFLINE_SHIM__"
const maxOfflineScannedEntries = 4096
const maxOfflineFileBytes = 32 << 20

// Logical names are relative to one runtime. Final runtime paths are assigned
// only after their bytes and the shim-inlined template enter runtimeBuildID.
type offlineBuildPlan struct {
	files       []offline.BuildFile
	publicStage string
	workerData  []byte
	publicLimit int
	bridge      string
	build       offline.Build
}

func offlineWorkerSource() string {
	return "package main\nimport \"" + framework + "/offline/worker\"\nfunc main(){worker.Run()}\n"
}

func compileOffline(ctx context.Context, p *Project, directory, tags string, support []byte, generated *buildassets.Result, moduleCount int, output io.Writer) (*offlineBuildPlan, error) {
	if p.Offline == "" {
		return nil, nil
	}
	bridge, err := inlineOfflineShim(worker.ScriptTemplate, support)
	if err != nil {
		return nil, err
	}
	plan := &offlineBuildPlan{bridge: bridge, publicStage: filepath.Join(directory, "offline-public"), publicLimit: offline.MaxAssets - 4 - moduleCount}
	public := filepath.Join(directory, "server", "assets", "public")
	if err := plan.snapshotPublic(ctx, p.Root, generated, public); err != nil {
		return nil, err
	}
	entry := filepath.Join(directory, "offline-worker")
	if err := os.MkdirAll(entry, 0755); err != nil {
		return nil, err
	}
	if err := writeGo(filepath.Join(entry, "main.go"), offlineWorkerSource()); err != nil {
		return nil, err
	}
	if err := validateOfflineWorkerGraph(ctx, p, entry, tags); err != nil {
		return nil, err
	}
	target, overlay, err := generatedEntryOverlay(ctx, p.Root, entry, "offline-worker")
	if err != nil {
		return nil, err
	}
	binary := filepath.Join(entry, "worker.wasm")
	if err := run(ctx, p.Root, output, []string{"GOOS=js", "GOARCH=wasm"}, "build", tags, "-overlay="+overlay, "-trimpath", "-ldflags=-s -w", "-o", binary, target); err != nil {
		return nil, err
	}
	data, err := readOfflineFile(ctx, binary)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 || string(data[:8]) != "\x00asm\x01\x00\x00\x00" {
		return nil, fmt.Errorf("offline worker is not a WASM module")
	}
	plan.workerData = data
	plan.files = append(plan.files, offlineRecord(offline.WorkerWASM, "worker-"+offlineSHA(data)+".wasm", data, "application/wasm"))
	return plan, nil
}

func inlineOfflineShim(template string, shim []byte) (string, error) {
	if strings.Count(template, offlineShimPlaceholder) != 1 || strings.Count(template, offline.BuildDescriptorPlaceholder) != 1 || len(shim) == 0 {
		return "", fmt.Errorf("offline worker bridge requires one paired shim and descriptor placeholder")
	}
	bridge := strings.Replace(template, offlineShimPlaceholder, string(shim), 1)
	if len(bridge) > offline.MaxBridgeBytes || strings.Count(bridge, offline.BuildDescriptorPlaceholder) != 1 || strings.Contains(bridge, offlineShimPlaceholder) {
		return "", fmt.Errorf("offline worker bridge exceeds its template bounds")
	}
	return bridge, nil
}

func offlineSHA(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func offlineRecord(kind offline.AssetKind, name string, data []byte, mime string) offline.BuildFile {
	return offline.BuildFile{Path: name, Kind: kind, SHA256: offlineSHA(data), DecodedBytes: int64(len(data)), ContentType: mime}
}

func readOfflineFile(ctx context.Context, name string) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxOfflineFileBytes {
		return nil, fmt.Errorf("offline asset must be a nonempty regular file of at most 32 MiB")
	}
	data, err := io.ReadAll(inputReader{ctx, io.LimitReader(file, info.Size()+1)})
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("offline asset changed while reading")
	}
	return data, nil
}

func (plan *offlineBuildPlan) snapshotPublic(ctx context.Context, app string, generated *buildassets.Result, public string) error {
	if err := os.MkdirAll(public, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(plan.publicStage, "assets"), 0755); err != nil {
		return err
	}
	seen := map[string]string{}
	var bytes int64
	scanned := 0
	copyRoot := func(source string) error {
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("offline public source must be a real directory")
		}
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
			scanned++
			if scanned > maxOfflineScannedEntries {
				return fmt.Errorf("offline public inventory exceeds 4096 scanned entries")
			}
			if name == "." {
				return nil
			}
			if !offlinePublicPath(name) || entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("offline public path must be a literal nonprivate path without symlinks: %s", name)
			}
			key := strings.ToLower(name)
			if old, ok := seen[key]; ok && (old != name || !entry.IsDir()) {
				return fmt.Errorf("offline public/generated asset collision: %s", name)
			}
			seen[key] = name
			if entry.IsDir() {
				return os.MkdirAll(filepath.Join(public, filepath.FromSlash(name)), 0755)
			}
			limit := plan.publicLimit
			if limit == 0 {
				limit = offline.MaxAssets - 4
			}
			if len(plan.files) >= limit {
				return fmt.Errorf("offline public inventory exceeds the complete runtime asset budget")
			}
			kind, mime, ok := offlinePublicKind(name)
			if !ok {
				return fmt.Errorf("unsupported offline public asset type: %s", name)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxOfflineFileBytes {
				return fmt.Errorf("offline public asset must be a nonempty regular file of at most 32 MiB")
			}
			file, err := root.Open(name)
			if err != nil {
				return err
			}
			opened, err := file.Stat()
			if err != nil {
				file.Close()
				return err
			}
			if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
				file.Close()
				return fmt.Errorf("offline public asset changed while opening")
			}
			data, readErr := io.ReadAll(inputReader{ctx, io.LimitReader(file, info.Size()+1)})
			closeErr := file.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if int64(len(data)) != info.Size() {
				return fmt.Errorf("offline public asset changed while reading")
			}
			bytes += int64(len(data))
			if bytes > offline.MaxBuildBytes {
				return fmt.Errorf("offline public snapshot exceeds 128 MiB")
			}
			if kind == offline.Style {
				if err := validateOfflineCSS(data); err != nil {
					return fmt.Errorf("offline stylesheet %s: %w", name, err)
				}
			}
			for _, base := range []string{public, filepath.Join(plan.publicStage, "assets")} {
				target := filepath.Join(base, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(target, data, 0644); err != nil {
					return err
				}
			}
			plan.files = append(plan.files, offlineRecord(kind, "offline-public/assets/"+name, data, mime))
			return nil
		})
	}
	if err := copyRoot(filepath.Join(app, "public")); err != nil {
		return err
	}
	if generated != nil {
		if err := copyRoot(generated.Dir); err != nil {
			return err
		}
	}
	style := false
	for _, file := range plan.files {
		style = style || file.Path == "offline-public/assets/style.css"
	}
	if !style {
		return fmt.Errorf("managed offline builds require a nonempty public/style.css")
	}
	return nil
}

func offlinePublicPath(name string) bool {
	if len(name) > offline.MaxURLBytes-80 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if part == "" || part == "." || part == ".." || lower == ".env" || strings.HasPrefix(lower, ".env.") {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
				return false
			}
		}
	}
	return true
}
func offlinePublicKind(name string) (offline.AssetKind, string, bool) {
	switch filepath.Ext(name) {
	case ".css":
		return offline.Style, "text/css; charset=utf-8", true
	case ".png":
		return offline.PublicAsset, "image/png", true
	case ".ico":
		return offline.PublicAsset, "image/x-icon", true
	case ".jpg", ".jpeg":
		return offline.PublicAsset, "image/jpeg", true
	case ".webp":
		return offline.PublicAsset, "image/webp", true
	case ".woff":
		return offline.PublicAsset, "font/woff", true
	case ".woff2":
		return offline.PublicAsset, "font/woff2", true
	default:
		return "", "", false
	}
}

func (plan *offlineBuildPlan) identity() string {
	if plan == nil {
		return ""
	}
	files := append([]offline.BuildFile(nil), plan.files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	encoded, _ := json.Marshal(struct {
		Version int
		Files   []offline.BuildFile
		Bridge  string
	}{offline.Version, files, plan.bridge})
	return "\x00offline:" + offlineSHA(encoded)
}
func (plan *offlineBuildPlan) finalize(runtime string, core, shim []byte, boot string, modules []lazyOutput) error {
	if plan == nil {
		return nil
	}
	files := append([]offline.BuildFile(nil), plan.files...)
	files = append(files, offlineRecord(offline.CoreWASM, "app.wasm", core, "application/wasm"), offlineRecord(offline.RuntimeShim, "wasm_exec.js", shim, "text/javascript; charset=utf-8"), offlineRecord(offline.BootScript, "boot.js", []byte(boot), "text/javascript; charset=utf-8"))
	for _, module := range modules {
		files = append(files, offlineRecord(offline.LazyWASM, module.File, module.Data, "application/wasm"))
	}
	for i := range files {
		files[i].Path = "_nextgo/" + runtime + "/" + files[i].Path
	}
	build, err := offline.NormalizeBuild(offline.Build{Version: offline.Version, RuntimeID: runtime, Files: files, BridgeTemplate: plan.bridge})
	if err != nil {
		return fmt.Errorf("offline compiler inventory: %w", err)
	}
	plan.build = build
	return nil
}
func (plan *offlineBuildPlan) publish(runtimeDir string) error {
	if plan == nil {
		return nil
	}
	for _, file := range plan.files {
		if file.Kind == offline.WorkerWASM {
			if err := os.WriteFile(filepath.Join(runtimeDir, file.Path), plan.workerData, 0644); err != nil {
				return err
			}
		}
	}
	return os.Rename(plan.publicStage, filepath.Join(runtimeDir, "offline-public"))
}

func writeOfflineConfiguration(b *strings.Builder, p *Project, aliases map[string]string) {
	if p.Offline == "" {
		return
	}
	b.WriteString("if config.Offline!=nil||config.OfflineBuild!=nil{return fmt.Errorf(\"managed offline policy must use app/offline.go rather than Configure\")}\n")
	fmt.Fprintf(b, "offlinePolicy:=%s.Offline();config.Offline=&offlinePolicy\n", aliases[p.Offline])
	b.WriteString("var offlineBuild offline.Build\n")
	if p.offlineBuild != nil {
		encoded, _ := json.Marshal(p.offlineBuild.build)
		fmt.Fprintf(b, "if err:=json.Unmarshal([]byte(%q),&offlineBuild);err!=nil{return err}\n", encoded)
	}
	b.WriteString("config.OfflineBuild=&offlineBuild\n")
}
