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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/metadatafiles"
)

type moduleVersion struct{ Path, Version string }
type moduleReplacement struct{ Old, New moduleVersion }
type moduleFile struct {
	Module  moduleVersion
	Replace []moduleReplacement
}
type moduleInfo struct{ Dir, Path, GoMod string }
type inputPlan struct {
	Roots, Files             []string
	GeneratedRoot            string
	MetadataRoot             string
	Overlays, OverlaySources map[string]string
}

// Go reports one JSON record per workspace main module. Select the module that
// owns this application rather than assuming the stream contains one record.
func applicationModule(ctx context.Context, root string) (moduleInfo, error) {
	data, err := goInputJSON(ctx, root, "env", "-json", "GOMOD")
	if err != nil {
		return moduleInfo{}, err
	}
	var env struct{ GOMOD string }
	if err := json.Unmarshal(data, &env); err != nil {
		return moduleInfo{}, err
	}
	return mainModule(ctx, root, env.GOMOD)
}
func mainModule(ctx context.Context, root, goMod string) (moduleInfo, error) {
	if goMod == "" || goMod == os.DevNull {
		return moduleInfo{}, fmt.Errorf("application must be inside a Go module")
	}
	owner := canonicalInput(filepath.Dir(goMod))
	data, err := goInputJSON(ctx, root, "list", "-m", "-json")
	if err != nil {
		return moduleInfo{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var mod moduleInfo
		err := decoder.Decode(&mod)
		if err == io.EOF {
			break
		}
		if err != nil {
			return moduleInfo{}, fmt.Errorf("invalid Go module metadata")
		}
		if mod.Dir == "" {
			continue
		}
		dir, e := filepath.EvalSymlinks(mod.Dir)
		if e == nil && dir == owner && mod.Path != "" {
			mod.Dir = dir
			return mod, nil
		}
	}
	return moduleInfo{}, fmt.Errorf("application must belong to a main Go module in the selected workspace")
}

type boundedInputOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *boundedInputOutput) Len() int       { return b.buffer.Len() }
func (b *boundedInputOutput) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedInputOutput) String() string { return b.buffer.String() }

func (b *boundedInputOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (2 << 20) - b.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
func goInputJSON(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.WaitDelay = time.Second
	output, diagnostic := &boundedInputOutput{}, &boundedInputOutput{}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(diagnostic.String())
		if len(detail) > 8192 {
			detail = detail[:8192] + "…"
		}
		return nil, fmt.Errorf("Go input metadata (%s): %w: %s", strings.Join(args, " "), err, detail)
	}
	if output.overflow {
		return nil, fmt.Errorf("Go input metadata exceeds 2 MiB")
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func canonicalInput(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// A symlinked manifest is still interpreted relative to its containing module
// or workspace path, not relative to the final symlink target's directory.
func manifestInput(base, name string) string {
	if !filepath.IsAbs(name) {
		name = filepath.Join(base, name)
	}
	return filepath.Join(canonicalInput(filepath.Dir(name)), filepath.Base(name))
}

func localInput(base, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return canonicalInput(path)
}
func addUnique(values *[]string, value string) {
	if value == "" {
		return
	}
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

// Local replacements in dependencies' go.mod files are intentionally not
// followed: Go only applies replacements from main modules and go.work.
func resolveInputs(ctx context.Context, root string) (inputPlan, error) {
	var plan inputPlan
	var first error
	record := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	plan.GeneratedRoot = filepath.Join(canonicalInput(root), ".nextgo", "generated")
	plan.MetadataRoot = filepath.Join(canonicalInput(root), "app")
	record(validateGeneratedVirtualRoot(canonicalInput(root)))
	overlay, overlayErr := readCallerOverlay(ctx, root)
	plan.Files = append(plan.Files, overlay.Files...)
	plan.Overlays, plan.OverlaySources = overlay.Replace, overlay.Sources
	record(overlayErr)
	data, err := goInputJSON(ctx, root, "env", "-json", "GOMOD", "GOWORK")
	if err != nil {
		return plan, err
	}
	var env struct{ GOMOD, GOWORK string }
	if err = json.Unmarshal(data, &env); err != nil {
		return plan, fmt.Errorf("invalid Go environment metadata")
	}
	if env.GOMOD == "" || env.GOMOD == os.DevNull {
		return plan, fmt.Errorf("application must be inside a Go module")
	}
	modulePaths := []string{manifestInput(root, env.GOMOD)}
	moduleBases := map[string]string{modulePaths[0]: canonicalInput(filepath.Dir(env.GOMOD))}
	selected, selectionErr := mainModule(ctx, root, env.GOMOD)
	record(selectionErr)
	if selectionErr == nil && selected.GoMod != "" {
		modulePaths = []string{manifestInput(root, selected.GoMod)}
		moduleBases[modulePaths[0]] = selected.Dir
	}
	addUnique(&plan.Roots, canonicalInput(filepath.Dir(env.GOMOD)))
	if env.GOWORK != "" && env.GOWORK != "off" {
		work := manifestInput(root, env.GOWORK)
		addUnique(&plan.Files, work)
		addUnique(&plan.Files, work+".sum")
		workData, err := goInputJSON(ctx, root, "work", "edit", "-json", work)
		record(err)
		if err == nil {
			var workspace struct {
				Use     []struct{ DiskPath string }
				Replace []moduleReplacement
			}
			err = json.Unmarshal(workData, &workspace)
			record(err)
			if err == nil {
				if len(workspace.Use) > 256 || len(workspace.Replace) > 256 {
					return plan, fmt.Errorf("workspace exceeds 256 module entries")
				}
				for _, use := range workspace.Use {
					dir := localInput(filepath.Dir(work), use.DiskPath)
					addUnique(&plan.Roots, dir)
					addUnique(&modulePaths, filepath.Join(dir, "go.mod"))
					moduleBases[filepath.Join(dir, "go.mod")] = dir
				}
				for _, replacement := range workspace.Replace {
					if replacement.New.Version == "" {
						addUnique(&plan.Roots, localInput(filepath.Dir(work), replacement.New.Path))
					}
				}
			}
		}
	}
	for _, path := range modulePaths {
		addUnique(&plan.Files, path)
		addUnique(&plan.Files, strings.TrimSuffix(path, ".mod")+".sum")
		modData, err := goInputJSON(ctx, root, "mod", "edit", "-json", path)
		record(err)
		if err != nil {
			continue
		}
		var mod moduleFile
		if err := json.Unmarshal(modData, &mod); err != nil {
			record(fmt.Errorf("invalid module file metadata"))
			continue
		}
		if len(mod.Replace) > 256 {
			return plan, fmt.Errorf("module exceeds 256 replacement entries")
		}
		for _, replacement := range mod.Replace {
			if replacement.New.Version == "" {
				addUnique(&plan.Roots, localInput(moduleBases[path], replacement.New.Path))
			}
		}
	}
	if len(plan.Roots) > 256 {
		return plan, fmt.Errorf("build inputs exceed 256 local module roots")
	}
	sort.Strings(plan.Roots)
	sort.Strings(plan.Files)
	return plan, first
}

// SourceWatcher keeps the previous input set through malformed manifests, so
// edits in already-known external modules can recover the last failed build.
// One development supervisor owns it; it is not safe for concurrent calls.
type SourceWatcher struct{ previous inputPlan }

func (w *SourceWatcher) Fingerprint(ctx context.Context, root string) (string, error) {
	plan, err := resolveInputs(ctx, root)
	if err != nil {
		for _, dir := range w.previous.Roots {
			addUnique(&plan.Roots, dir)
		}
		seenFiles := make(map[string]bool, len(plan.Files)+len(w.previous.Files))
		for _, file := range plan.Files {
			seenFiles[file] = true
		}
		for _, file := range w.previous.Files {
			if !seenFiles[file] {
				seenFiles[file] = true
				plan.Files = append(plan.Files, file)
			}
		}
		if plan.Overlays == nil {
			plan.Overlays = map[string]string{}
		}
		if plan.OverlaySources == nil {
			plan.OverlaySources = map[string]string{}
		}
		for from, to := range w.previous.Overlays {
			if _, ok := plan.Overlays[from]; !ok {
				plan.Overlays[from] = to
			}
		}
		for from, to := range w.previous.OverlaySources {
			if _, ok := plan.OverlaySources[from]; !ok {
				plan.OverlaySources[from] = to
			}
		}
	} else {
		w.previous = plan
	}
	value, hashErr := hashInputsContext(ctx, plan, "")
	if hashErr != nil {
		return value, hashErr
	}
	return value, err
}

const maxInputFile = 256 << 20
const maxInputTotal = 2 << 30
const maxInputFiles = 100000

type inputReader struct {
	ctx    context.Context
	source io.Reader
}

func (r inputReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func hashInputs(plan inputPlan, skip string) (string, error) {
	return hashInputsContext(context.Background(), plan, skip)
}
func hashInputsContext(ctx context.Context, plan inputPlan, skip string) (string, error) {
	hash := sha256.New()
	skipped := map[string]bool{}
	if skip != "" {
		skipped[skip] = true
		// Input-only snapshots exclude exactly our four published Go outputs.
		// Full build/watch fingerprints pass no skip and include every output.
		if filepath.Base(skip) == "nextgo_gen.go" && filepath.Base(filepath.Dir(skip)) == "routes" {
			for _, generated := range actionFiles(filepath.Dir(filepath.Dir(skip))) {
				skipped[generated] = true
			}
		}
	}
	seen := map[string]bool{}
	var total int64
	record := func(name string, kind byte, size int64) error {
		if len(seen) >= maxInputFiles || size > maxInputFile || total+size > maxInputTotal {
			return fmt.Errorf("build inputs exceed file/count/total byte limits")
		}
		seen[name] = true
		total += size
		fmt.Fprintf(hash, "%d:%s%c%d:", len(name), name, kind, size)
		return nil
	}
	var embeds []struct{ dir, pattern string }
	scanned := map[string]bool{}
	var consume func(string, bool, bool) error
	consume = func(name string, allowMissing, followLink bool) error {
		if skipped[name] || seen[name] {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if os.IsNotExist(err) && allowMissing {
			return record(name, 'M', 0)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			linkName := name
			if followLink {
				linkName += "\x00link"
			}
			if err := record(linkName, 'L', int64(len(target))); err != nil {
				return err
			}
			_, _ = hash.Write([]byte(target))
			if !followLink {
				return nil
			}
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("build input must be a regular file: %s", name)
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return err
		}
		if !opened.Mode().IsRegular() {
			return fmt.Errorf("build input changed type: %s", name)
		}
		if err := record(name, 'F', opened.Size()); err != nil {
			return err
		}
		reader := io.LimitReader(f, opened.Size()+1)
		n, err := io.Copy(hash, inputReader{ctx, reader})
		if err != nil {
			return err
		}
		if n != opened.Size() {
			return fmt.Errorf("build input changed size: %s", name)
		}
		return nil
	}
	sort.Strings(plan.Roots)
	sort.Strings(plan.Files)
	if plan.GeneratedRoot != "" {
		kind := byte('M')
		info, err := os.Lstat(plan.GeneratedRoot)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil {
			kind = 'F'
			if info.IsDir() {
				kind = 'D'
			} else if info.Mode()&os.ModeSymlink != 0 {
				kind = 'L'
			}
		}
		if err := record(plan.GeneratedRoot, kind, 0); err != nil {
			return "", err
		}
	}
	for _, file := range plan.Files {
		if err := consume(file, true, true); err != nil {
			return "", err
		}
	}
	for _, moduleRoot := range plan.Roots {
		if _, err := os.Stat(moduleRoot); os.IsNotExist(err) {
			if !seen[moduleRoot] {
				if err := record(moduleRoot, 'D', 0); err != nil {
					return "", err
				}
			}
			continue
		}
		err := filepath.WalkDir(moduleRoot, func(name string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if skipped[name] {
				return nil
			}
			relative, err := filepath.Rel(moduleRoot, name)
			if err != nil {
				return err
			}
			publicAsset := strings.Contains("/"+filepath.ToSlash(relative)+"/", "/public/")
			if d.IsDir() {
				if name != moduleRoot && ignored(d.Name()) && !publicAsset {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() == assetPipelineManifest {
				if err := hashAssetPipelineInputs(ctx, filepath.Dir(name), hash); err != nil {
					return err
				}
			}
			ext := filepath.Ext(name)
			metadataInput := false
			if filepath.Dir(name) == plan.MetadataRoot {
				for _, known := range metadatafiles.Names() {
					metadataInput = metadataInput || strings.EqualFold(d.Name(), known)
				}
			}
			if ext != ".go" && ext != ".mod" && ext != ".sum" && filepath.Base(name) != "component.json" && !publicAsset && !metadataInput {
				return nil
			}
			if err := consume(name, false, !publicAsset); err != nil {
				return err
			}
			_, overlaid := overlayBacking(plan.Overlays, name)
			if ext == ".go" && !scanned[name] && !overlaid {
				scanned[name] = true
				file, err := os.Open(name)
				if err != nil {
					return err
				}
				data, err := io.ReadAll(inputReader{ctx, io.LimitReader(file, maxInputFile+1)})
				_ = file.Close()
				if err != nil {
					return err
				}
				if len(data) > maxInputFile {
					return fmt.Errorf("Go source exceeds build input limit")
				}
				for _, pattern := range embeddedPatterns(name, data) {
					if len(embeds) >= 10000 {
						return fmt.Errorf("build inputs exceed 10000 embed patterns")
					}
					embeds = append(embeds, struct{ dir, pattern string }{filepath.Dir(name), pattern})
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	overlaySources := make([]string, 0, len(plan.OverlaySources))
	for name := range plan.OverlaySources {
		overlaySources = append(overlaySources, name)
	}
	sort.Strings(overlaySources)
	for _, name := range overlaySources {
		backing := plan.OverlaySources[name]
		file, err := os.Open(backing)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(inputReader{ctx, io.LimitReader(file, maxInputFile+1)})
		_ = file.Close()
		if err != nil {
			return "", err
		}
		if len(data) > maxInputFile {
			return "", fmt.Errorf("overlaid Go source exceeds build input limit")
		}
		for _, pattern := range embeddedPatterns(name, data) {
			if len(embeds) >= 10000 {
				return "", fmt.Errorf("build inputs exceed 10000 embed patterns")
			}
			embeds = append(embeds, struct{ dir, pattern string }{filepath.Dir(name), pattern})
		}
	}
	for _, embed := range embeds {
		if err := expandEmbeddedOverlay(embed.dir, embed.pattern, plan.Overlays, func(name string) error { return consume(name, false, false) }); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
