package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

// DevRefreshBuild is compiler-proven, development-only public module metadata.
// Compatibility excludes only selected component-local Go inputs proved absent
// from the native and core browser graphs. Source paths and values are private.
// Aggregate zones intentionally do not expose refresh metadata in this version.
type DevRefreshBuild struct {
	Version       int
	Compatibility string
	ShimDigest    string
	ModuleABI     int
	Modules       []devrefresh.Module
}

type devRefreshPlan struct {
	compatibility string
	epochs        map[string]string
	inputs        inputPlan
	inputDigest   string
	graphDigest   string
	queries       []refreshGraphQuery
	contextDigest string
	invariant     string
	invariantRows map[string]string
}

var errDevRefreshProofChanged = errors.New("development refresh proof changed during build")

type refreshGraphQuery struct {
	name, target, overlay, tags string
	browser                     bool
}

// These are cmd/go's selected compilation inputs, not inferred filename/build
// tag guesses. Export archives additionally bind native CGO/toolchain-selected
// code to what the compiler actually built, without forcing different flags.
type refreshGoPackage struct {
	Dir, ImportPath, Name, Export, BuildID string
	GoFiles, CgoFiles                      []string
	CFiles, CXXFiles, MFiles, HFiles       []string
	FFiles, SFiles, SwigFiles              []string
	SwigCXXFiles, SysoFiles, EmbedFiles    []string
	Incomplete                             bool
	Error                                  *struct{ Err string }
	DepsErrors                             []struct{ Err string }
	Module                                 *struct {
		Path, Version, Dir, GoMod string
		Replace                   *struct{ Path, Version, Dir, GoMod string }
	}
}

type refreshSelectedGraph struct {
	goFiles  map[string]bool
	files    map[string]bool
	packages map[string]refreshGoPackage
	rows     map[string]string
}

func refreshDigest(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func hasRefreshComponents(p *Project) bool {
	for _, component := range p.Components {
		if component.Lazy && component.Refresh != nil {
			return true
		}
	}
	return false
}

func refreshComponentGraphName(name string) string { return "module:" + name }

func prepareDevRefresh(ctx context.Context, p *Project, buildDir, coreTags, moduleTags string, support []byte) (*devRefreshPlan, error) {
	sum := sha256.Sum256(support)
	if !hasRefreshComponents(p) || hex.EncodeToString(sum[:]) != devrefresh.ShimDigest {
		return nil, nil
	}
	inputs, contextDigest, err := devRefreshInputs(ctx, p.Root, coreTags, moduleTags)
	if err != nil {
		return nil, err
	}
	zero := strings.Repeat("0", 64)
	var templateModules []lazyOutput
	var queries []refreshGraphQuery
	for index, component := range p.Components {
		if !component.Lazy {
			continue
		}
		module := lazyOutput{Name: component.Name, File: "module-" + zero + ".wasm", Digest: zero, Epoch: zero, Size: 1}
		if component.Refresh != nil {
			module.StateSchema, module.DOMSchema = component.Refresh.StateSchema, component.Refresh.DOMSchema
			if err := (devrefresh.Module{Name: component.Name, URL: "/_nextgo/" + strings.Repeat("0", 20) + "/" + module.File, Digest: zero, Size: 1, Epoch: zero, StateSchema: module.StateSchema, DOMSchema: module.DOMSchema}).Validate(); err != nil {
				return nil, fmt.Errorf("component identity is unsupported by development refresh")
			}
		}
		templateModules = append(templateModules, module)
		query, err := refreshProbe(ctx, p, buildDir, fmt.Sprintf("refresh-module-%d", index), lazyMainSource(component, zero, true), moduleTags, true, false)
		if err != nil {
			return nil, err
		}
		query.name = refreshComponentGraphName(component.Name)
		queries = append(queries, query)
	}
	nativeSource := serverSource(p, strings.Repeat("0", 20), true, true)
	coreSource := clientSourceRefresh(p, templateModules, zero)
	native, err := refreshProbe(ctx, p, buildDir, "refresh-native", nativeSource, "", false, true)
	if err != nil {
		return nil, err
	}
	core, err := refreshProbe(ctx, p, buildDir, "refresh-core", coreSource, coreTags, true, false)
	if err != nil {
		return nil, err
	}
	queries = append([]refreshGraphQuery{native, core}, queries...)
	return collectDevRefreshPlan(ctx, p, inputs, queries, contextDigest, nativeSource, coreSource)
}

func devRefreshInputs(ctx context.Context, root, coreTags, moduleTags string) (inputPlan, string, error) {
	inputs, err := resolveInputs(ctx, root)
	if err != nil {
		return inputs, "", err
	}
	contextDigest, toolFiles, err := refreshBuildContext(ctx, root, coreTags, moduleTags)
	if err != nil {
		return inputs, "", err
	}
	for _, file := range toolFiles {
		addUnique(&inputs.Files, file)
	}
	// All local non-code inputs are conservative invariants too. Ordinary source
	// watching retains its existing contract; this proof never grants eligibility
	// to an unclassified input merely because it was absent from a compiler graph.
	if err := appendRefreshNonCode(ctx, &inputs); err != nil {
		return inputs, "", err
	}
	return inputs, contextDigest, nil
}

func collectDevRefreshPlan(ctx context.Context, p *Project, inputs inputPlan, queries []refreshGraphQuery, contextDigest, nativeSource, coreSource string) (*devRefreshPlan, error) {
	graphs, graphDigest, err := refreshGraphs(ctx, p, queries)
	if err != nil {
		return nil, err
	}
	eligible := refreshEligibleFiles(p, graphs, inputs.Overlays)
	excluded := make(map[string]bool, len(eligible))
	for file := range eligible {
		excluded[file] = true
	}
	for name, graph := range graphs {
		if name == "native" || name == "core" {
			continue
		}
		for importPath, pkg := range graph.packages {
			if pkg.Export != "" && refreshPackageHasEligible(graph, importPath, eligible) {
				excluded[pkg.Export] = true
			}
		}
	}
	inputs.Excluded = excluded
	for _, graph := range graphs {
		for file := range graph.files {
			addUnique(&inputs.Files, file)
		}
	}
	allInputs := inputs
	allInputs.Excluded = nil
	inputDigest, err := hashInputsContext(ctx, allInputs, "")
	if err != nil {
		return nil, err
	}
	invariant, err := hashInputsContext(ctx, inputs, "")
	if err != nil {
		return nil, err
	}
	// The generated entry package contains changing content addresses/epochs.
	// Its exact stable template is bound independently; all imported compiler
	// selected packages remain in the proof. Non-opted modules are invariants.
	invariantRows := map[string]string{}
	for file := range eligible {
		shape, err := refreshTypeShape(ctx, file)
		if err != nil {
			return nil, err
		}
		invariantRows["types:"+file] = shape
	}
	for name, graph := range graphs {
		for key, value := range graph.rows {
			if strings.HasPrefix(key, "source:") && eligible[strings.TrimPrefix(key, "source:")] {
				continue
			}
			if strings.HasPrefix(key, "archive:") && name != "native" && name != "core" && refreshPackageHasEligible(graph, strings.TrimPrefix(key, "archive:"), eligible) {
				continue
			}
			if strings.HasPrefix(key, "package:") && name != "native" && name != "core" && refreshPackageHasEligible(graph, strings.TrimPrefix(key, "package:"), eligible) {
				continue
			}
			invariantRows[name+":"+key] = value
		}
	}
	plan := &devRefreshPlan{epochs: map[string]string{}, inputs: allInputs, inputDigest: inputDigest, graphDigest: graphDigest, queries: queries, contextDigest: contextDigest, invariant: invariant, invariantRows: invariantRows}
	zero := strings.Repeat("0", 64)
	for _, component := range p.Components {
		if component.Lazy {
			plan.epochs[component.Name] = refreshDigest(struct {
				Context string
				Source  string
				Rows    map[string]string
			}{contextDigest, lazyMainSource(component, zero, true), graphs[refreshComponentGraphName(component.Name)].rows})
		}
	}
	plan.compatibility = refreshDigest(struct {
		Version, ABI       int
		Context, Invariant string
		Native, Core       string
		Rows               map[string]string
	}{devrefresh.Version, devrefresh.ModuleABI, contextDigest, invariant, nativeSource, coreSource, invariantRows})
	return plan, nil
}

// Even browser-only source cannot refresh across a props/type contract change.
// Keep declarations (including aliases, type parameters and tags) and anonymous
// struct/interface shapes, wherever declared. Comments and executable bodies
// do not enter this invariant; the selected compiler graph remains authoritative
// for ownership. Imports bind the meaning of qualified names in these shapes.
func refreshTypeShape(ctx context.Context, name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(inputReader{ctx, io.LimitReader(file, maxInputFile+1)})
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > maxInputFile {
		return "", fmt.Errorf("development refresh type input unavailable")
	}
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, name, data, 0)
	if err != nil {
		return "", fmt.Errorf("development refresh type input invalid")
	}
	var shapes []string
	var shapeErr error
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.TypeSpec, *ast.StructType, *ast.InterfaceType, *ast.ImportSpec:
			// A symbolic array bound can depend on a constant in another
			// eligible file. AST spelling alone cannot prove that semantic type
			// stable, so this entire optional proof falls back to normal reloads.
			ast.Inspect(node, func(part ast.Node) bool {
				if array, ok := part.(*ast.ArrayType); ok && array.Len != nil {
					if _, literal := array.Len.(*ast.BasicLit); !literal {
						shapeErr = errors.New("symbolic type shape")
					}
				}
				return true
			})
			var text bytes.Buffer
			if err := format.Node(&text, positions, node); err != nil {
				shapeErr = err
				return false
			}
			shapes = append(shapes, text.String())
		}
		return true
	})
	if shapeErr != nil {
		return "", fmt.Errorf("development refresh type input invalid")
	}
	return refreshDigest(shapes), nil
}

func refreshProbe(ctx context.Context, p *Project, buildDir, kind, source, tags string, browser, embedded bool) (refreshGraphQuery, error) {
	dir := filepath.Join(buildDir, kind)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return refreshGraphQuery{}, err
	}
	if embedded {
		for _, name := range []string{"assets/placeholder", "private/placeholder"} {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return refreshGraphQuery{}, err
			}
			if err := os.WriteFile(path, nil, 0600); err != nil {
				return refreshGraphQuery{}, err
			}
		}
	}
	if err := writeGo(filepath.Join(dir, "main.go"), source); err != nil {
		return refreshGraphQuery{}, err
	}
	target, overlay, err := generatedEntryOverlay(ctx, p.Root, dir, kind)
	return refreshGraphQuery{name: strings.TrimPrefix(kind, "refresh-"), target: target, overlay: overlay, tags: tags, browser: browser}, err
}

func refreshGoList(ctx context.Context, root string, query refreshGraphQuery) ([]refreshGoPackage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	fields := "Dir,ImportPath,Name,Export,BuildID,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,Incomplete,Error,DepsErrors,Module"
	args := []string{"list", "-deps", "-export", "-json=" + fields, "-trimpath", "-ldflags=-s -w", "-overlay=" + query.overlay}
	if query.tags != "" {
		args = append(args, query.tags)
	}
	args = append(args, query.target)
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	command.WaitDelay = time.Second
	command.Env = command.Environ()
	if query.browser {
		command.Env = refreshBrowserEnvironment(command.Env)
	}
	output, diagnostic := &boundedInputOutput{}, &boundedInputOutput{}
	command.Stdout, command.Stderr = output, diagnostic
	if err := command.Run(); err != nil || output.overflow {
		return nil, fmt.Errorf("development refresh selected compiler graph unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var packages []refreshGoPackage
	for {
		var pkg refreshGoPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			return packages, nil
		} else if err != nil || pkg.Incomplete || pkg.Error != nil || len(pkg.DepsErrors) != 0 {
			return nil, fmt.Errorf("development refresh selected compiler graph invalid")
		}
		packages = append(packages, pkg)
	}
}

func refreshBrowserEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, value := range environment {
		if !strings.HasPrefix(value, "GOOS=") && !strings.HasPrefix(value, "GOARCH=") {
			result = append(result, value)
		}
	}
	return append(result, "GOOS=js", "GOARCH=wasm")
}

func refreshGraphs(ctx context.Context, p *Project, queries []refreshGraphQuery) (map[string]refreshSelectedGraph, string, error) {
	graphs := map[string]refreshSelectedGraph{}
	allRows := map[string]map[string]string{}
	digests := map[string]string{}
	caller, err := readCallerOverlay(ctx, p.Root)
	if err != nil {
		return nil, "", err
	}
	for _, query := range queries {
		packages, err := refreshGoList(ctx, p.Root, query)
		if err != nil {
			return nil, "", err
		}
		graph := refreshSelectedGraph{goFiles: map[string]bool{}, files: map[string]bool{}, packages: map[string]refreshGoPackage{}, rows: map[string]string{}}
		for _, pkg := range packages {
			// Only these compiler-owned entry packages are omitted. Their imports
			// are queried by Go and their templates are hashed by the caller.
			if pathWithin(canonicalInput(pkg.Dir), filepath.Join(p.Root, ".nextgo", "generated")) {
				continue
			}
			graph.packages[pkg.ImportPath] = pkg
			graph.rows["package:"+pkg.ImportPath] = refreshDigest(struct {
				Dir, Path, BuildID string
				Go, Cgo            []string
				Module             any
			}{canonicalInput(pkg.Dir), pkg.ImportPath, pkg.BuildID, pkg.GoFiles, pkg.CgoFiles, pkg.Module})
			goInputs := append(append([]string(nil), pkg.GoFiles...), pkg.CgoFiles...)
			other := [][]string{pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles}
			for _, names := range append([][]string{goInputs}, other...) {
				for _, name := range names {
					logical := name
					if !filepath.IsAbs(logical) {
						logical = filepath.Join(pkg.Dir, name)
					}
					logical = canonicalInput(logical)
					physical, _ := overlayBacking(caller.Replace, logical)
					if physical == "" {
						return nil, "", fmt.Errorf("selected refresh input is deleted by overlay")
					}
					digest, err := refreshFileDigest(ctx, physical, digests)
					if err != nil {
						return nil, "", err
					}
					graph.files[physical] = true
					graph.rows["source:"+logical] = digest
				}
			}
			for _, name := range goInputs {
				if !filepath.IsAbs(name) {
					name = filepath.Join(pkg.Dir, name)
				}
				graph.goFiles[canonicalInput(name)] = true
			}
			if pkg.Export != "" {
				digest, err := refreshFileDigest(ctx, pkg.Export, digests)
				if err != nil {
					return nil, "", err
				}
				graph.files[pkg.Export] = true
				graph.rows["archive:"+pkg.ImportPath] = digest
			}
			if pkg.Module != nil {
				for _, file := range []string{pkg.Module.GoMod, func() string {
					if pkg.Module.Replace != nil {
						return pkg.Module.Replace.GoMod
					}
					return ""
				}()} {
					if file != "" {
						graph.files[file] = true
					}
				}
			}
		}
		graphs[query.name] = graph
		allRows[query.name] = graph.rows
	}
	return graphs, refreshDigest(allRows), nil
}

func refreshFileDigest(ctx context.Context, name string, cache map[string]string) (string, error) {
	if digest, ok := cache[name]; ok {
		return digest, nil
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxInputFile {
		return "", fmt.Errorf("development refresh compiler input exceeds file limit")
	}
	hash := sha256.New()
	count, err := io.Copy(hash, inputReader{ctx, io.LimitReader(file, info.Size()+1)})
	if err != nil || count != info.Size() {
		return "", fmt.Errorf("development refresh compiler input changed while reading")
	}
	value := hex.EncodeToString(hash.Sum(nil))
	cache[name] = value
	return value, nil
}

func refreshEligibleFiles(p *Project, graphs map[string]refreshSelectedGraph, overlays map[string]string) map[string]bool {
	owners := map[string][]string{}
	for _, component := range p.Components {
		if component.Lazy {
			for file := range graphs[refreshComponentGraphName(component.Name)].goFiles {
				owners[file] = append(owners[file], component.Name)
			}
		}
	}
	eligible := map[string]bool{}
	for _, component := range p.Components {
		if component.Refresh == nil || !component.Lazy {
			continue
		}
		root := filepath.Join(p.Root, "components", filepath.FromSlash(component.Name))
		for file := range graphs[refreshComponentGraphName(component.Name)].goFiles {
			base := filepath.Base(file)
			if len(owners[file]) != 1 || graphs["native"].goFiles[file] || graphs["core"].goFiles[file] || !pathWithin(file, root) || filepath.Ext(file) != ".go" || base == "props.go" || base == "server.go" || strings.HasSuffix(base, "_test.go") {
				continue
			}
			if _, overlaid := overlayBacking(overlays, file); overlaid {
				continue
			}
			info, err := os.Lstat(file)
			if err != nil || !info.Mode().IsRegular() || info.Size() > maxInputFile {
				continue
			}
			opened, err := os.Open(file)
			if err != nil {
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(opened, maxInputFile+1))
			_ = opened.Close()
			if readErr != nil || len(data) > maxInputFile || len(embeddedPatterns(file, data)) != 0 {
				continue
			}
			eligible[file] = true
		}
	}
	return eligible
}

func refreshPackageHasEligible(graph refreshSelectedGraph, importPath string, eligible map[string]bool) bool {
	pkg := graph.packages[importPath]
	for _, name := range append(append([]string(nil), pkg.GoFiles...), pkg.CgoFiles...) {
		if !filepath.IsAbs(name) {
			name = filepath.Join(pkg.Dir, name)
		}
		if eligible[canonicalInput(name)] {
			return true
		}
	}
	return false
}

func appendRefreshNonCode(ctx context.Context, plan *inputPlan) error {
	seen := make(map[string]bool, len(plan.Files))
	for _, file := range plan.Files {
		seen[file] = true
	}
	for _, root := range plan.Roots {
		if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				if name != root && ignored(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			// Environment ownership is separately checked by the supervisor before
			// and after native startup; values never enter public proof metadata.
			if filepath.Ext(name) == ".go" || environmentInput(name) || seen[name] {
				return nil
			}
			if !entry.Type().IsRegular() || len(seen) >= maxInputFiles {
				return fmt.Errorf("unclassified refresh input type/count")
			}
			seen[name] = true
			plan.Files = append(plan.Files, name)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func refreshBuildContext(ctx context.Context, root, coreTags, moduleTags string) (string, []string, error) {
	data, err := goInputJSON(ctx, root, "env", "-json")
	if err != nil {
		return "", nil, err
	}
	var environment map[string]string
	if err := json.Unmarshal(data, &environment); err != nil {
		return "", nil, err
	}
	// cmd/go's read-only GOGCCFLAGS includes a fresh go-build<digits> temporary
	// prefix on each `go env` invocation. Normalize only that compiler-generated
	// reproducibility mapping; retain every actual flag and caller CGO_* value.
	environment["GOGCCFLAGS"] = stableRefreshGCCFlags(environment["GOGCCFLAGS"], environment["GOTMPDIR"])
	var files []string
	for _, file := range []string{filepath.Join(environment["GOROOT"], "bin", "go"), filepath.Join(environment["GOTOOLDIR"], "compile"), filepath.Join(environment["GOTOOLDIR"], "link"), filepath.Join(environment["GOTOOLDIR"], "asm"), filepath.Join(environment["GOTOOLDIR"], "cgo")} {
		if _, err := os.Stat(file); err == nil {
			files = append(files, file)
		} else if !os.IsNotExist(err) {
			return "", nil, err
		}
	}
	if name := environment["GOENV"]; name != "" && name != "off" {
		if _, err := os.Stat(name); err == nil {
			files = append(files, name)
		} else if !os.IsNotExist(err) {
			return "", nil, err
		}
	}
	return refreshDigest(struct {
		Environment  map[string]string
		Core, Module string
		Shim, Bridge string
	}{environment, coreTags, moduleTags, devrefresh.ShimDigest, devRefreshBootstrapBridge}), files, nil
}

func stableRefreshGCCFlags(value, temp string) string {
	words, err := goFlagWords(value)
	if err != nil {
		return value
	}
	if temp == "" {
		temp = os.TempDir()
	}
	for index, word := range words {
		for _, prefix := range []string{"-ffile-prefix-map=", "-fdebug-prefix-map="} {
			mapping, ok := strings.CutPrefix(word, prefix)
			if !ok {
				continue
			}
			from, ok := strings.CutSuffix(mapping, "=/tmp/go-build")
			if !ok || canonicalInput(filepath.Dir(from)) != canonicalInput(temp) {
				continue
			}
			suffix, ok := strings.CutPrefix(filepath.Base(from), "go-build")
			if !ok || suffix == "" || strings.IndexFunc(suffix, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				continue
			}
			words[index] = prefix + "$GO_BUILD_WORKDIR=/tmp/go-build"
		}
	}
	return strings.Join(words, "\x00")
}

func (plan *devRefreshPlan) validate(ctx context.Context, p *Project, coreTags, moduleTags string) error {
	if plan == nil {
		return nil
	}
	contextDigest, _, err := refreshBuildContext(ctx, p.Root, coreTags, moduleTags)
	if err != nil || contextDigest != plan.contextDigest {
		return errDevRefreshProofChanged
	}
	_, graphDigest, err := refreshGraphs(ctx, p, plan.queries)
	if err != nil || graphDigest != plan.graphDigest {
		return errDevRefreshProofChanged
	}
	currentInputs := plan.inputs
	currentInputs.Files = append([]string(nil), plan.inputs.Files...)
	if err := appendRefreshNonCode(ctx, &currentInputs); err != nil {
		return errDevRefreshProofChanged
	}
	current, err := hashInputsContext(ctx, currentInputs, "")
	if err != nil || current != plan.inputDigest {
		return errDevRefreshProofChanged
	}
	return nil
}

func (plan *devRefreshPlan) build(id string, modules []lazyOutput) (*DevRefreshBuild, error) {
	if plan == nil {
		return nil, nil
	}
	result := &DevRefreshBuild{Version: devrefresh.Version, Compatibility: plan.compatibility, ShimDigest: devrefresh.ShimDigest, ModuleABI: devrefresh.ModuleABI}
	for _, module := range modules {
		if module.StateSchema == "" {
			continue
		}
		descriptor := devrefresh.Module{Name: module.Name, URL: "/_nextgo/" + id + "/" + module.File, Digest: module.Digest, Size: module.Size, Epoch: module.Epoch, StateSchema: module.StateSchema, DOMSchema: module.DOMSchema}
		if err := descriptor.Validate(); err != nil {
			return nil, err
		}
		result.Modules = append(result.Modules, descriptor)
	}
	sort.Slice(result.Modules, func(i, j int) bool { return result.Modules[i].Name < result.Modules[j].Name })
	if len(result.Modules) == 0 || len(result.Modules) > devrefresh.MaxModules {
		return nil, fmt.Errorf("invalid compiler refresh module inventory")
	}
	return result, nil
}
