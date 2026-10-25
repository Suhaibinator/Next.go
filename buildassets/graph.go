//go:build !js

package buildassets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxDependencyEdges = 256

func declaredOutput(output string, names map[string]bool, directories map[string]string) error {
	key := strings.ToLower(output)
	for old := range names {
		if old == key || strings.HasPrefix(old, key+"/") || strings.HasPrefix(key, old+"/") {
			return fmt.Errorf("asset output collision: %s", output)
		}
	}
	names[key] = true
	for parent := filepath.ToSlash(filepath.Dir(output)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
		key := strings.ToLower(parent)
		if old, ok := directories[key]; ok && old != parent {
			return fmt.Errorf("asset directory case collision: %s", parent)
		}
		directories[key] = parent
	}
	return nil
}

func dependencyAlias(alias string) bool {
	if len(alias) < 1 || len(alias) > 64 || safePath(alias, true) != nil {
		return false
	}
	for i, c := range []byte(alias) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// dependencyOrder validates the complete graph before any tool runs. Scanning
// ready nodes by manifest index keeps otherwise-independent tool order stable.
func dependencyOrder(p Pipeline) ([]int, error) {
	byName := make(map[string]int, len(p.Tools))
	publishers := 0
	for i, tool := range p.Tools {
		byName[tool.Name] = i
		if !tool.Intermediate {
			publishers++
		}
	}
	if publishers == 0 {
		return nil, fmt.Errorf("asset pipeline version 2 requires a publishing tool")
	}
	pending := make([]int, len(p.Tools))
	downstream := make([][]int, len(p.Tools))
	edges := 0
	for i, tool := range p.Tools {
		if len(tool.Dependencies) > 64 {
			return nil, fmt.Errorf("asset tool exceeds 64 dependency aliases")
		}
		aliases := map[string]bool{}
		for alias, producer := range tool.Dependencies {
			if !dependencyAlias(alias) {
				return nil, fmt.Errorf("invalid asset dependency alias")
			}
			key := strings.ToLower(alias)
			if aliases[key] {
				return nil, fmt.Errorf("case-colliding asset dependency alias")
			}
			aliases[key] = true
			if len(producer) < 1 || len(producer) > 128 {
				return nil, fmt.Errorf("invalid asset dependency producer name")
			}
			before, ok := byName[producer]
			if !ok || before == i {
				return nil, fmt.Errorf("asset dependency must name a different declared tool")
			}
			edges++
			if edges > maxDependencyEdges {
				return nil, fmt.Errorf("asset pipeline exceeds 256 dependency edges")
			}
			pending[i]++
			downstream[before] = append(downstream[before], i)
		}
	}
	order := make([]int, 0, len(p.Tools))
	used := make([]bool, len(p.Tools))
	for len(order) < len(p.Tools) {
		ready := -1
		for i := range p.Tools {
			if !used[i] && pending[i] == 0 {
				ready = i
				break
			}
		}
		if ready < 0 {
			return nil, fmt.Errorf("asset tool dependencies contain a cycle")
		}
		used[ready] = true
		order = append(order, ready)
		for _, next := range downstream[ready] {
			pending[next]--
		}
	}
	return order, nil
}

func snapshotDependencies(ctx context.Context, p Pipeline, tool Tool, verified map[int]string, destination string, q *quota) error {
	byName := make(map[string]int, len(p.Tools))
	for i, candidate := range p.Tools {
		byName[candidate.Name] = i
	}
	aliases := make([]string, 0, len(tool.Dependencies))
	for alias := range tool.Dependencies {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if !dependencyAlias(alias) {
			return fmt.Errorf("invalid asset dependency alias")
		}
		producer, ok := byName[tool.Dependencies[alias]]
		if !ok {
			return fmt.Errorf("asset dependency is not declared")
		}
		source := verified[producer]
		if source == "" {
			return fmt.Errorf("asset dependency is not verified")
		}
		target := filepath.Join(destination, alias)
		if err := os.Mkdir(target, 0700); err != nil {
			return err
		}
		if err := collectWithQuota(ctx, source, target, p.Tools[producer].Outputs, q); err != nil {
			return err
		}
	}
	return nil
}

// Freeze configuration after bounded validation. A typed Transform may close
// over its caller's Pipeline; changes there cannot change later graph edges,
// publication declarations or argv after preflight.
func cloneGraphPipeline(p Pipeline) Pipeline {
	result := Pipeline{Version: p.Version, Tools: make([]Tool, len(p.Tools))}
	for i, tool := range p.Tools {
		tool.Command = append([]string(nil), tool.Command...)
		tool.Inputs = append([]string(nil), tool.Inputs...)
		tool.Outputs = append([]string(nil), tool.Outputs...)
		dependencies := make(map[string]string, len(tool.Dependencies))
		for alias, name := range tool.Dependencies {
			dependencies[alias] = name
		}
		tool.Dependencies = dependencies
		environment := make(map[string]string, len(tool.Env))
		for key, value := range tool.Env {
			environment[key] = value
		}
		tool.Env = environment
		result.Tools[i] = tool
	}
	return result
}
