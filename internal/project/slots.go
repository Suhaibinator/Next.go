package project

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/routing"
)

func discoverSlots(p *Project, appDir string) error {
	return discoverSlotTree(p, appDir, p.Import+"/app", "/", 0)
}
func discoverSlotTree(p *Project, appDir, appImport, prefix string, depth int) error {
	if depth > navigation.MaxSlotDepth {
		return fmt.Errorf("named slot directory depth exceeds %d", navigation.MaxSlotDepth)
	}
	if depth == 0 && p.Default != "" && p.owners[appImport].Import == "" {
		return fmt.Errorf("primary default.go requires layout.go at its owner")
	}
	owners := map[string][]Slot{}
	err := filepath.WalkDir(appDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if path != appDir && ignored(d.Name()) {
			return filepath.SkipDir
		}
		if path != appDir && d.Name() == "intercept_root" {
			return filepath.SkipDir
		}
		if path == appDir || !strings.HasPrefix(d.Name(), "slot_") {
			return nil
		}
		name := strings.TrimPrefix(d.Name(), "slot_")
		if _, err := routing.Validate("/:" + name); err != nil || name == "children" {
			return fmt.Errorf("invalid named slot directory %s", path)
		}
		owner := filepath.Dir(path)
		if _, err := os.Stat(filepath.Join(owner, "layout.go")); err != nil {
			return fmt.Errorf("slot %s requires layout.go in its parent", path)
		}
		relative, _ := filepath.Rel(appDir, owner)
		relativeBase, err := Pattern(relative)
		if err != nil {
			return err
		}
		base := slotPattern(prefix, relativeBase)
		ownerImport := appImport
		if relative != "." {
			ownerImport += "/" + filepath.ToSlash(relative)
		}
		subtree := &Project{}
		if _, err := discoverApp(subtree, path, ownerImport+"/"+d.Name(), base); err != nil {
			return err
		}
		if err := prefixTree(subtree, base); err != nil {
			return err
		}
		if err := discoverSlotTree(subtree, path, ownerImport+"/"+d.Name(), base, depth+1); err != nil {
			return err
		}
		for _, branch := range subtree.Routes {
			if branch.API || branch.StaticParams || len(branch.StaticParamsLayers) > 0 {
				return fmt.Errorf("slot %s supports pages, not API/static parameter callbacks", path)
			}
		}
		intercepts := []Route{}
		interceptDir := filepath.Join(path, "intercept_root")
		if _, err := os.Stat(interceptDir); err == nil {
			if depth != 0 {
				return fmt.Errorf("interception owners inside named slots are not supported: %s", path)
			}
			targets := &Project{}
			if _, err := discoverApp(targets, interceptDir, ownerImport+"/"+d.Name()+"/intercept_root", "/"); err != nil {
				return err
			}
			if err := discoverSlotTree(targets, interceptDir, ownerImport+"/"+d.Name()+"/intercept_root", "/", depth+1); err != nil {
				return err
			}
			for _, branch := range targets.Routes {
				if branch.API || branch.StaticParams || len(branch.StaticParamsLayers) > 0 {
					return fmt.Errorf("interception %s supports pages without API/static enumeration", path)
				}
			}
			intercepts = targets.Routes
		}
		owners[ownerImport] = append(owners[ownerImport], Slot{ID: ownerImport + "#" + name, Name: name, Base: base, Default: subtree.Default, DefaultRoute: subtree.defaultRoute, Routes: subtree.Routes, Intercepts: intercepts})
		return filepath.SkipDir
	})
	if err != nil {
		return err
	}
	keys := []string{}
	for owner := range owners {
		keys = append(keys, owner)
	}
	sort.Strings(keys)
	implicitOwners := map[string]bool{}
	type contribution struct{ owner, pattern string }
	contributions := []contribution{}
	patternsByShape := map[string]string{}
	for _, owner := range keys {
		implicitOwners[owner] = p.owners[owner].Default
		for _, slot := range owners[owner] {
			implicitOwners[owner] = implicitOwners[owner] || len(slot.Intercepts) > 0
			for _, branch := range slot.Routes {
				contributions = append(contributions, contribution{owner, branch.Pattern})
				shape, err := routing.Validate(branch.Pattern)
				if err != nil {
					return err
				}
				if _, ok := patternsByShape[shape]; !ok {
					patternsByShape[shape] = branch.Pattern
				}
			}
		}
	}
	patterns := []string{}
	for _, pattern := range patternsByShape {
		patterns = append(patterns, pattern)
	}
	sort.SliceStable(patterns, func(i, j int) bool { return routing.Less(patterns[i], patterns[j]) })
	realRoutes := append([]Route(nil), p.Routes...)
	sort.SliceStable(realRoutes, func(i, j int) bool { return routing.Less(realRoutes[i].Pattern, realRoutes[j].Pattern) })
	containsOwner := func(route Route, owner string) bool {
		for _, id := range route.LayoutIDs {
			if id == owner {
				return true
			}
		}
		return false
	}
	for _, pattern := range patterns {
		required := map[string]bool{}
		for _, claim := range contributions {
			if _, covers := routing.Match(claim.pattern, pattern); covers {
				required[claim.owner] = true
			}
		}
		matched := false
		for _, primary := range realRoutes {
			if _, covers := routing.Match(primary.Pattern, pattern); !covers {
				continue
			}
			if primary.API {
				return fmt.Errorf("parallel URL %s overlaps HTTP endpoint %s", pattern, primary.Pattern)
			}
			compatible := true
			for owner := range required {
				if !containsOwner(primary, owner) {
					compatible = false
					break
				}
			}
			if !compatible {
				oldShape, _ := routing.Shape(primary.Pattern)
				newShape, _ := routing.Shape(pattern)
				if oldShape != newShape && routing.Less(pattern, primary.Pattern) {
					continue
				}
				return fmt.Errorf("parallel URL %s is owned by an incompatible primary layout", pattern)
			}
			matched = true
			break
		}
		if matched {
			continue
		}
		chosen := ""
		for _, owner := range keys {
			if !required[owner] {
				continue
			}
			candidate := p.owners[owner]
			compatible := true
			for needed := range required {
				if !containsOwner(candidate, needed) {
					compatible = false
					break
				}
			}
			if compatible && (chosen == "" || len(candidate.Layouts) > len(p.owners[chosen].Layouts)) {
				chosen = owner
			}
		}
		if chosen == "" {
			return fmt.Errorf("parallel URL %s has incompatible layout owners", pattern)
		}
		route := p.owners[chosen]
		route.Pattern = pattern
		index := -1
		for i, id := range route.LayoutIDs {
			if id == chosen {
				index = i
				break
			}
		}
		route.Primary = &PrimarySlot{ID: chosen + "#children", Base: owners[chosen][0].Base, Layout: index, Fallback: true}
		p.Routes = append(p.Routes, route)
		implicitOwners[chosen] = true
	}
	// Every composite URL exists before attachment. This lets outer named
	// slots participate in URLs contributed by a deeper parallel owner.
	for _, owner := range keys {
		slots := owners[owner]
		implicit := implicitOwners[owner]
		attached := false
		targets := []*Route{}
		for i := range p.Routes {
			targets = append(targets, &p.Routes[i])
		}
		if p.defaultRoute != nil {
			targets = append(targets, p.defaultRoute)
		}
		for _, route := range targets {
			if route.API {
				continue
			}
			for index, id := range route.LayoutIDs {
				if id == owner {
					if implicit {
						fallback := route == p.defaultRoute
						for _, existing := range projectPrimaries(*route) {
							if existing.ID == owner+"#children" {
								fallback = fallback || existing.Fallback
							}
						}
						addPrimary(route, PrimarySlot{ID: owner + "#children", Base: slots[0].Base, Layout: index, Fallback: fallback})
					}
					for _, slot := range slots {
						slot.Layout = index
						route.Slots = append(route.Slots, slot)
					}
					attached = true
					break
				}
			}
		}
		if !attached {
			return fmt.Errorf("slot owner %s has no URL-producing page", owner)
		}
	}
	for owner, route := range p.owners {
		if route.Default && len(owners[owner]) == 0 && !(depth > 0 && owner == appImport) {
			return fmt.Errorf("primary default.go at %s requires named slots", owner)
		}
	}
	patterns = nil
	for _, r := range p.Routes {

		patterns = append(patterns, r.Pattern)
	}
	return routing.ValidatePatterns(patterns)
}
func slotPattern(base, relative string) string {
	if base == "/" {
		return relative
	}
	if relative == "/" {
		return base
	}
	return base + relative
}

// Convert a discovered subtree once, before recursively composing child slots.
// Every registered pattern and template uses the application's logical URL.
func prefixTree(p *Project, prefix string) error {
	apply := func(r *Route) error {
		r.Pattern = slotPattern(prefix, r.Pattern)
		for i := range r.StaticParamsLayers {
			r.StaticParamsLayers[i].Pattern = slotPattern(prefix, r.StaticParamsLayers[i].Pattern)
		}
		if _, err := routing.Validate(r.Pattern); err != nil {
			return err
		}
		for i, base := range r.LayoutBases {
			r.LayoutBases[i] = slotPattern(prefix, base)
		}
		for i, pattern := range r.Templates {
			r.Templates[i] = slotPattern(prefix, pattern)
		}
		return nil
	}
	for i := range p.Routes {
		if err := apply(&p.Routes[i]); err != nil {
			return err
		}
	}
	if p.defaultRoute != nil {
		if err := apply(p.defaultRoute); err != nil {
			return err
		}
	}
	for imp, r := range p.owners {
		if err := apply(&r); err != nil {
			return err
		}
		p.owners[imp] = r
	}
	return nil
}

func projectPrimaries(r Route) []PrimarySlot {
	if r.Primary != nil {
		return []PrimarySlot{*r.Primary}
	}
	return r.Primaries
}
func addPrimary(r *Route, p PrimarySlot) {
	chain := append([]PrimarySlot(nil), projectPrimaries(*r)...)
	replaced := false
	for i := range chain {
		if chain[i].ID == p.ID {
			chain[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		chain = append(chain, p)
	}
	sort.SliceStable(chain, func(i, j int) bool { return chain[i].Layout < chain[j].Layout })
	if len(chain) == 1 {
		r.Primary = &chain[0]
		r.Primaries = nil
	} else {
		r.Primary = nil
		r.Primaries = chain
	}
}
