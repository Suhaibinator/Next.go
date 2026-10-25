// Package project discovers conventional Go application files and builds entrypoints.
package project

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/internal/routing"
)

type AccessBoundary struct {
	Unauthorized, Forbidden string
	Layouts                 int
}
type StaticParamsLayer struct {
	Import, Pattern string
}
type MetadataLayer struct {
	Import  string
	Layouts int
}
type Route struct {
	Pattern, Import  string
	Default, Missing bool
	Primary          *PrimarySlot
	Primaries        []PrimarySlot
	Slots            []Slot
	Layouts          []string
	LayoutBases      []string
	LayoutIDs        []string
	// Templates maps wrapper indexes to their visible directory pattern.
	Templates          map[int]string
	NotFound           string
	Access             []AccessBoundary
	Metadata           []MetadataLayer
	Error              string
	Loading            string
	Prerender          bool
	Regions            bool
	StaticParamsLayers []StaticParamsLayer
	StaticParams       bool
	API                bool
}
type PrimarySlot struct {
	ID, Base string
	Layout   int
	Fallback bool
}

type Slot struct {
	ID, Name, Base, Default string
	Layout                  int
	Routes                  []Route
	Intercepts              []Route
	DefaultRoute            *Route
}
type Component struct {
	Name, Import string
	Lazy         bool
}
type Project struct {
	Root, ModuleDir, ModulePath, Import, NotFound, Configure string
	Unauthorized, Forbidden                                  string
	Default                                                  string
	defaultRoute                                             *Route
	owners                                                   map[string]Route
	Routes                                                   []Route
	Components                                               []Component
	Actions                                                  []Action
}

func ignored(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist"
}

func Discover(root string) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mod, err := applicationModule(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("read application module: %w", err)
	}
	rel, err := filepath.Rel(mod.Dir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("app must be inside its Go module")
	}
	imp := mod.Path
	if rel != "." {
		imp += "/" + filepath.ToSlash(rel)
	}
	p := &Project{Root: abs, ModuleDir: mod.Dir, ModulePath: mod.Path, Import: imp}
	p.Actions, err = DiscoverActions(p)
	if err != nil {
		return nil, err
	}
	appDir := filepath.Join(abs, "app")
	if _, err = discoverApp(p, appDir, imp+"/app", "/"); err != nil {
		return nil, err
	}
	if err = discoverSlots(p, appDir); err != nil {
		return nil, err
	}
	if err = validateFileInterceptions(p); err != nil {
		return nil, err
	}
	if err = validateRegionRoutes(p); err != nil {
		return nil, err
	}
	componentsDir := filepath.Join(abs, "components")
	if _, e := os.Stat(componentsDir); e == nil {
		err = filepath.WalkDir(componentsDir, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				if path != componentsDir && ignored(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "client_js.go" {
				return nil
			}
			rel, _ := filepath.Rel(componentsDir, filepath.Dir(path))
			if rel == "." {
				return fmt.Errorf("put browser components in named subdirectories")
			}
			name := filepath.ToSlash(rel)
			lazy, err := lazyComponent(filepath.Dir(path))
			if err != nil {
				return fmt.Errorf("component %s: %w", name, err)
			}
			p.Components = append(p.Components, Component{Name: name, Import: imp + "/components/" + name, Lazy: lazy})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(p.Routes, func(i, j int) bool { return p.Routes[i].Pattern < p.Routes[j].Pattern })
	lazyCount := 0
	for _, component := range p.Components {
		if component.Lazy {
			lazyCount++
		}
	}
	if lazyCount > maxLazyComponents {
		return nil, fmt.Errorf("at most eight lazy components are supported")
	}
	sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].Name < p.Components[j].Name })
	return p, nil
}

func Pattern(relative string) (string, error) {
	if relative == "." || relative == "" {
		return "/", nil
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		// Go import paths cannot contain the bracket syntax used by Next.js.
		if strings.ContainsAny(part, "[]{} ") {
			return "", fmt.Errorf("invalid Go route directory %q; use param_name for parameters", part)
		}
		switch {
		case strings.HasPrefix(part, "group_"):
			if part == "group_" {
				return "", fmt.Errorf("route group requires a name")
			}
			continue
		case strings.HasPrefix(part, "param_"):
			part = ":" + strings.TrimPrefix(part, "param_")
		case strings.HasPrefix(part, "catchall_"):
			part = "*" + strings.TrimPrefix(part, "catchall_")
		case strings.HasPrefix(part, "optional_"):
			part = "*" + strings.TrimPrefix(part, "optional_") + "?"
		}
		segments = append(segments, part)
	}
	p := "/" + strings.Join(segments, "/")
	_, err := routing.Validate(p)
	return p, err
}

func discoverApp(p *Project, appDir, appImport, prefix string) (*Project, error) {
	layouts := map[string]string{}
	templates := map[string]string{}
	notFound := map[string]string{}
	unauthorized := map[string]string{}
	forbidden := map[string]string{}
	errors := map[string]string{}
	loading := map[string]string{}
	metadata := map[string]string{}
	paths := map[string]string{}
	staticParams := map[string]string{}
	claimedStaticParams := map[string]bool{}
	prerender := map[string]string{}
	regions := map[string]string{}
	defaults := map[string]bool{}
	hasSlots := false
	err := filepath.WalkDir(appDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != appDir && (ignored(d.Name()) || strings.HasPrefix(d.Name(), "slot_") || d.Name() == "intercept_root") {
				if d.Name() == "intercept_root" && (filepath.Dir(path) != appDir || !strings.HasPrefix(filepath.Base(appDir), "slot_")) {
					return fmt.Errorf("intercept_root must be directly inside a slot directory: %s", path)
				}
				if strings.HasPrefix(d.Name(), "slot_") {
					hasSlots = true
				}
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		if base != "default.go" && base != "page.go" && base != "layout.go" && base != "template.go" && base != "route.go" && base != "notfound.go" && base != "unauthorized.go" && base != "forbidden.go" && base != "error.go" && base != "loading.go" && base != "metadata.go" && base != "config.go" && base != "staticparams.go" && base != "prerender.go" && base != "regions.go" {
			return nil
		}
		if base == "config.go" && filepath.Dir(path) != appDir {
			return nil
		}
		if _, e := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly); e != nil {
			return e
		}
		dir := filepath.Dir(path)
		relative, _ := filepath.Rel(appDir, dir)
		importPath := appImport
		if relative != "." {
			importPath += "/" + filepath.ToSlash(relative)
		}
		if base == "default.go" {
			defaults[dir] = true
			if dir == appDir {
				p.Default = importPath
			}
			return nil
		}
		if base == "metadata.go" {
			metadata[dir] = importPath
			return nil
		}
		if base == "prerender.go" {
			prerender[importPath] = path
			return nil
		}
		if base == "regions.go" {
			if strings.HasPrefix(filepath.Base(appDir), "slot_") || filepath.Base(appDir) == "intercept_root" {
				return fmt.Errorf("%s supports ordinary pages, not named-slot or interception registrations", path)
			}
			regions[importPath] = path
			return nil
		}
		if base == "staticparams.go" {
			staticParams[importPath] = path
			return nil
		}
		if base == "config.go" {
			p.Configure = importPath
			return nil
		}
		if base == "template.go" {
			templates[dir] = importPath
			return nil
		}
		if base == "layout.go" {
			layouts[dir] = importPath
			return nil
		}
		if base == "notfound.go" {
			notFound[dir] = importPath
			if relative == "." {
				p.NotFound = importPath
			}
			return nil
		}
		if base == "unauthorized.go" || base == "forbidden.go" {
			if base == "unauthorized.go" {
				unauthorized[dir] = importPath
				if relative == "." {
					p.Unauthorized = importPath
				}
			} else {
				forbidden[dir] = importPath
				if relative == "." {
					p.Forbidden = importPath
				}
			}
			return nil
		}
		if base == "error.go" {
			errors[dir] = importPath
			return nil
		}
		if base == "loading.go" {
			loading[dir] = importPath
			return nil
		}
		pattern, e := Pattern(relative)
		if e != nil {
			return e
		}
		globalPattern := slotPattern(prefix, pattern)
		api := base == "route.go"
		if !api && (globalPattern == "/api" || strings.HasPrefix(globalPattern, "/api/")) {
			return fmt.Errorf("pages cannot use the reserved /api prefix")
		}
		if strings.HasPrefix(globalPattern, "/_nextgo") || globalPattern == "/assets" || strings.HasPrefix(globalPattern, "/assets/") {
			return fmt.Errorf("reserved route %s", pattern)
		}
		_, e = routing.Validate(pattern)
		if e != nil {
			return e
		}
		for previous, previousPath := range paths {
			if routing.Conflicts(previous, pattern) {
				return fmt.Errorf("ambiguous routes: %s and %s", previousPath, path)
			}
		}
		paths[pattern] = path
		p.Routes = append(p.Routes, Route{Pattern: pattern, Import: importPath, API: api})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(p.Routes) == 0 && len(defaults) == 0 && !hasSlots {
		if len(regions) > 0 {
			return nil, fmt.Errorf("regions.go requires page.go in the same directory")
		}
		return nil, fmt.Errorf("no pages or API routes in %s", appDir)
	}
	for i := range p.Routes {
		r := &p.Routes[i]
		if file := regions[r.Import]; file != "" {
			if r.API {
				return nil, fmt.Errorf("%s requires page.go in the same directory", file)
			}
			r.Regions = true
			delete(regions, r.Import)
		}
		if file := prerender[r.Import]; file != "" {
			if r.API {
				return nil, fmt.Errorf("%s requires page.go in the same directory", file)
			}
			r.Prerender = true
			delete(prerender, r.Import)
		}
		if file := staticParams[r.Import]; file != "" {
			if r.API || !strings.ContainsAny(r.Pattern, ":*") {
				return nil, fmt.Errorf("%s requires a dynamic page.go in the same directory", file)
			}
			r.StaticParams = true
			claimedStaticParams[r.Import] = true
		}
		if r.API {
			continue
		}
		relative := strings.TrimPrefix(r.Import, appImport)
		routeDir := filepath.Join(appDir, filepath.FromSlash(relative))
		for dir := filepath.Dir(routeDir); dir != filepath.Dir(appDir); dir = filepath.Dir(dir) {
			rel, _ := filepath.Rel(appDir, dir)
			imp := appImport
			if rel != "." {
				imp += "/" + filepath.ToSlash(rel)
			}
			if file := staticParams[imp]; file != "" {
				pattern, err := Pattern(rel)
				if err != nil {
					return nil, err
				}
				if !strings.ContainsAny(pattern, ":*") {
					return nil, fmt.Errorf("%s requires a dynamic directory prefix", file)
				}
				r.StaticParamsLayers = append(r.StaticParamsLayers, StaticParamsLayer{Import: imp, Pattern: pattern})
				claimedStaticParams[imp] = true
			}
		}
		for left, right := 0, len(r.StaticParamsLayers)-1; left < right; left, right = left+1, right-1 {
			r.StaticParamsLayers[left], r.StaticParamsLayers[right] = r.StaticParamsLayers[right], r.StaticParamsLayers[left]
		}
	}
	enrich := func(r *Route) error {
		// Imported route directories correspond exactly to discovered on-disk paths.
		rel := strings.TrimPrefix(r.Import, appImport)
		dir := filepath.Join(appDir, filepath.FromSlash(rel))
		type wrapper struct{ imp, templatePattern, base string }
		var chain []wrapper
		var access []AccessBoundary
		var providers []MetadataLayer
		for {
			if metadata[dir] != "" {
				providers = append(providers, MetadataLayer{Import: metadata[dir]})
			}
			if dir != appDir && (unauthorized[dir] != "" || forbidden[dir] != "") {
				access = append(access, AccessBoundary{Unauthorized: unauthorized[dir], Forbidden: forbidden[dir]})
			}
			if r.NotFound == "" {
				r.NotFound = notFound[dir]
			}
			if r.Error == "" {
				r.Error = errors[dir]
			}
			if r.Loading == "" {
				r.Loading = loading[dir]
			}
			if template := templates[dir]; template != "" {
				relative, _ := filepath.Rel(appDir, dir)
				prefix, err := Pattern(relative)
				if err != nil {
					return err
				}
				chain = append(chain, wrapper{imp: template, templatePattern: prefix, base: prefix})
			}
			if layout := layouts[dir]; layout != "" {
				relative, _ := filepath.Rel(appDir, dir)
				base, err := Pattern(relative)
				if err != nil {
					return err
				}
				chain = append(chain, wrapper{imp: layout, base: base})
			}
			if dir == appDir {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return fmt.Errorf("layout traversal escaped app root")
			}
			dir = parent
		}
		for j := len(chain) - 1; j >= 0; j-- {
			w := chain[j]
			index := len(r.Layouts)
			r.Layouts = append(r.Layouts, w.imp)
			r.LayoutBases = append(r.LayoutBases, w.base)
			if w.templatePattern != "" {
				if r.Templates == nil {
					r.Templates = make(map[int]string)
				}
				r.Templates[index] = w.templatePattern
				r.LayoutIDs = append(r.LayoutIDs, "")
			} else {
				r.LayoutIDs = append(r.LayoutIDs, w.imp)
			}
		}
		for j := len(providers) - 1; j >= 0; j-- {
			layer := providers[j]
			for _, wrapper := range r.Layouts {
				if strings.HasPrefix(layer.Import, wrapper+"/") {
					layer.Layouts++
				}
			}
			r.Metadata = append(r.Metadata, layer)
		}
		for j := len(access) - 1; j >= 0; j-- {
			boundary := access[j]
			owner := boundary.Unauthorized
			if owner == "" {
				owner = boundary.Forbidden
			}
			for _, wrapper := range r.Layouts {
				if strings.HasPrefix(owner, wrapper+"/") {
					boundary.Layouts++
				}
			}
			r.Access = append(r.Access, boundary)
		}
		return nil
	}
	for i := range p.Routes {
		if !p.Routes[i].API {
			if err := enrich(&p.Routes[i]); err != nil {
				return nil, err
			}
		}
	}
	if p.Default != "" {
		fallback := Route{Pattern: "/", Import: appImport, Default: true}
		if err := enrich(&fallback); err != nil {
			return nil, err
		}
		p.defaultRoute = &fallback
	}
	p.owners = map[string]Route{}
	for dir, imp := range layouts {
		relative, _ := filepath.Rel(appDir, dir)
		pattern, err := Pattern(relative)
		if err != nil {
			return nil, err
		}
		owner := Route{Pattern: pattern, Import: imp, Default: defaults[dir], Missing: !defaults[dir]}
		if err := enrich(&owner); err != nil {
			return nil, err
		}
		p.owners[imp] = owner
		delete(defaults, dir)
	}
	// A named-slot root may have a default without its own layout.
	delete(defaults, appDir)
	if len(defaults) > 0 {
		return nil, fmt.Errorf("primary default.go requires layout.go in the same directory")
	}

	if len(prerender) > 0 {
		return nil, fmt.Errorf("prerender.go requires page.go in the same directory")
	}
	if len(regions) > 0 {
		return nil, fmt.Errorf("regions.go requires page.go in the same directory")
	}
	for imp := range claimedStaticParams {
		delete(staticParams, imp)
	}
	if len(staticParams) > 0 {
		files := make([]string, 0, len(staticParams))
		for _, file := range staticParams {
			files = append(files, file)
		}
		sort.Strings(files)
		return nil, fmt.Errorf("%s requires a dynamic page.go in the same directory", files[0])
	}
	return p, nil
}
