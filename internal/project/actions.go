package project

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

// Action is a declaratively discovered, explicitly versioned mutation. Discovery
// parses source and never executes application code or package initializers.
type Action struct{ Name, Revision, Import, Symbol string }

// DiscoverActions reads actions/<name>/contract.go and its native action.go.
// Fixed names make generated bindings ordinary Go compiler-checked code.
func DiscoverActions(p *Project) ([]Action, error) {
	root := filepath.Join(p.Root, "actions")
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("actions directory must be a real directory: %s", root)
	}
	var result []Action
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("actions cannot contain symlinks: %s", path)
		}
		if d.IsDir() {
			if path != root && ignored(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			return fmt.Errorf("actions cannot use a nested module: %s", path)
		}
		if d.Name() != "contract.go" && d.Name() != "action.go" {
			return nil
		}
		dir := filepath.Dir(path)
		name, _ := filepath.Rel(root, dir)
		name = filepath.ToSlash(name)
		if name == "." || len(name) > 256 || !actionwire.ValidName(name) {
			return fmt.Errorf("invalid action directory %q", name)
		}
		if d.Name() == "action.go" {
			if _, err := os.Lstat(filepath.Join(dir, "contract.go")); err != nil {
				return fmt.Errorf("action %s needs shared contract.go: %w", name, err)
			}
			return nil
		}
		a, err := readAction(dir, name, p.Import+"/actions/"+name)
		if err != nil {
			return fmt.Errorf("action %s: %w", name, err)
		}
		result = append(result, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if err := validateActions(result); err != nil {
		return nil, err
	}
	return result, nil
}

func parseActionFile(path string) (*ast.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("action source must be a regular file at most 1 MiB: %s", path)
	}
	return parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
}

func readAction(dir, name, imp string) (Action, error) {
	shared, err := parseActionFile(filepath.Join(dir, "contract.go"))
	if err != nil {
		return Action{}, err
	}
	native, err := parseActionFile(filepath.Join(dir, "action.go"))
	if err != nil {
		return Action{}, fmt.Errorf("needs native action.go: %w", err)
	}
	if shared.Name.Name != native.Name.Name || shared.Name.Name == "main" {
		return Action{}, fmt.Errorf("contract.go and action.go must use the same importable package")
	}
	if buildConstraint(shared) != "" {
		return Action{}, fmt.Errorf("contract.go must be shared without a build constraint")
	}
	if buildConstraint(native) != "!js" {
		return Action{}, fmt.Errorf("action.go requires //go:build !js")
	}
	mutationAlias := ""
	for _, spec := range shared.Imports {
		value, _ := strconv.Unquote(spec.Path.Value)
		if value == framework+"/mutation" {
			mutationAlias = "mutation"
			if spec.Name != nil {
				mutationAlias = spec.Name.Name
			}
		}
	}
	if mutationAlias == "" || mutationAlias == "." || mutationAlias == "_" {
		return Action{}, fmt.Errorf("contract.go must import mutation by name")
	}
	marker := false
	for _, spec := range native.Imports {
		value, _ := strconv.Unquote(spec.Path.Value)
		if value == framework+"/serveronly" && spec.Name != nil && spec.Name.Name == "_" {
			marker = true
		}
	}
	if !marker {
		return Action{}, fmt.Errorf("action.go must blank-import %s/serveronly", framework)
	}
	types := map[string]bool{}
	var contract ast.Expr
	for _, decl := range shared.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, raw := range gen.Specs {
			switch spec := raw.(type) {
			case *ast.TypeSpec:
				types[spec.Name.Name] = true
			case *ast.ValueSpec:
				for _, id := range spec.Names {
					if id.Name == "Contract" {
						if gen.Tok != token.VAR || len(spec.Names) != 1 || len(spec.Values) != 1 || contract != nil {
							return Action{}, fmt.Errorf("Contract must be one variable initialized with mutation.Must[Input, Output]")
						}
						contract = spec.Values[0]
					}
				}
			}
		}
	}
	if !types["Input"] || !types["Output"] {
		return Action{}, fmt.Errorf("contract.go must declare Input and Output types")
	}
	call, ok := contract.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || call.Ellipsis.IsValid() {
		return Action{}, fmt.Errorf("Contract must use mutation.Must[Input, Output](literalName, literalRevision)")
	}
	index, ok := call.Fun.(*ast.IndexListExpr)
	if !ok || len(index.Indices) != 2 {
		return Action{}, fmt.Errorf("Contract requires Input and Output type arguments")
	}
	selector, ok := index.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Must" {
		return Action{}, fmt.Errorf("Contract must use mutation.Must")
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok || alias.Name != mutationAlias {
		return Action{}, fmt.Errorf("Contract must use the imported mutation.Must")
	}
	for i, want := range []string{"Input", "Output"} {
		id, ok := index.Indices[i].(*ast.Ident)
		if !ok || id.Name != want {
			return Action{}, fmt.Errorf("Contract type arguments must be Input, Output")
		}
	}
	literals := make([]string, 2)
	for i, argument := range call.Args {
		literal, ok := argument.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return Action{}, fmt.Errorf("action name and revision must be string literals")
		}
		literals[i], err = strconv.Unquote(literal.Value)
		if err != nil {
			return Action{}, err
		}
	}
	if literals[0] != name {
		return Action{}, fmt.Errorf("contract name %q must match directory %q", literals[0], name)
	}
	if len(literals[1]) > 64 || !actionwire.ValidSegment(literals[1]) {
		return Action{}, fmt.Errorf("invalid contract revision %q", literals[1])
	}
	functions := map[string]bool{}
	for _, decl := range native.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			functions[fn.Name.Name] = true
		}
	}
	if !functions["Handle"] || !functions["Options"] {
		return Action{}, fmt.Errorf("action.go must export Handle and Options functions")
	}
	return Action{Name: name, Revision: literals[1], Import: imp, Symbol: routeIdentifier(name)}, nil
}

func buildConstraint(file *ast.File) string {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:build ") {
				return strings.TrimSpace(strings.TrimPrefix(comment.Text, "//go:build "))
			}
			if strings.HasPrefix(comment.Text, "// +build ") {
				return "legacy build constraint"
			}
		}
	}
	return ""
}

func actionSymbols(a Action) []string {
	return []string{a.Symbol, a.Symbol + "Multipart", a.Symbol + "AndRefresh", a.Symbol + "MultipartAndRefresh", a.Symbol + "Path", a.Symbol + "Input", a.Symbol + "Output"}
}
func validateActions(actions []Action) error {
	names := map[string]bool{}
	symbols := map[string]string{"Register": "generated registrar"}
	for _, a := range actions {
		key := a.Name + "/" + a.Revision
		if names[key] {
			return fmt.Errorf("duplicate action endpoint %q", key)
		}
		names[key] = true
		for _, symbol := range actionSymbols(a) {
			if old, ok := symbols[symbol]; ok {
				return fmt.Errorf("action symbol collision %q between %q and %q", symbol, old, a.Name)
			}
			symbols[symbol] = a.Name
		}
	}
	return nil
}
