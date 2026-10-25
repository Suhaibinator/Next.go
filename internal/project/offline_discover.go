package project

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// The native convention selects a worker build without evaluating application
// code in the compiler. Its policy is evaluated once by the generated server.
func discoverOffline(p *Project) (string, error) {
	app := filepath.Join(p.Root, "app")
	name := filepath.Join(app, "offline.go")
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64<<10 {
		return "", fmt.Errorf("app/offline.go must be a regular file of at most 64 KiB")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return "", fmt.Errorf("app/offline.go changed while opening")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 64<<10+1))
	closeErr := file.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if len(data) > 64<<10 || int64(len(data)) != info.Size() {
		return "", fmt.Errorf("app/offline.go changed or exceeds 64 KiB")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), name, data, 0)
	if err != nil {
		return "", err
	}
	packages := map[string]bool{}
	for _, imp := range parsed.Imports {
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return "", err
		}
		if value == framework+"/offline" {
			alias := "offline"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			packages[alias] = true
		}
	}
	found := false
	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Offline" || fn.Recv != nil {
			continue
		}
		if found || fn.Type.Params.NumFields() != 0 || fn.Type.TypeParams.NumFields() != 0 || fn.Type.Results.NumFields() != 1 {
			return "", fmt.Errorf("app/offline.go must export func Offline() offline.Config")
		}
		result, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
		if !ok || result.Sel.Name != "Config" {
			return "", fmt.Errorf("app/offline.go must export func Offline() offline.Config")
		}
		alias, ok := result.X.(*ast.Ident)
		if !ok || !packages[alias.Name] {
			return "", fmt.Errorf("app/offline.go must return github.com/Suhaibinator/Next.go/offline.Config")
		}
		found = true
	}
	if !found {
		return "", fmt.Errorf("app/offline.go must export func Offline() offline.Config")
	}
	return p.Import + "/app", nil
}

func validateOfflinePurpose(p *Project, dev, allowPrerender, release bool) error {
	if p.Offline == "" {
		return nil
	}
	if dev {
		return fmt.Errorf("managed offline builds support production only; development and HMR are unavailable")
	}
	if !allowPrerender || release {
		return fmt.Errorf("managed offline builds are unavailable for aggregate or independent zones")
	}
	if hasPrerenderFiles(p) {
		return fmt.Errorf("managed offline builds do not support prerender declarations")
	}
	if len(p.MetadataFiles) != 0 {
		return fmt.Errorf("managed offline builds do not support managed metadata resources")
	}
	return nil
}
