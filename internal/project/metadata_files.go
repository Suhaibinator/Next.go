package project

import (
	"context"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/metadatafiles"
)

// MetadataFile identifies a single managed app-root resource. Import/Factory
// select a native typed startup producer; otherwise Source is a static file.
// The generator must compile each producer as the matching Sources field.
type MetadataFile struct {
	Name    string
	Source  string
	Import  string
	Factory string
	Digest  string
	Size    int64
}

func (m MetadataFile) Static() bool { return m.Factory == "" }

var metadataProducers = map[string]struct{ name, factory string }{
	"robots.go":   {"robots.txt", "Robots"},
	"sitemap.go":  {"sitemap.xml", "Sitemap"},
	"manifest.go": {"manifest.webmanifest", "Manifest"},
}

// DiscoverMetadataFiles parses only root conventions and snapshots static
// descriptors. It never executes a Go producer or evaluates configuration.
func DiscoverMetadataFiles(p *Project) ([]MetadataFile, error) {
	app := filepath.Join(p.Root, "app")
	info, err := os.Lstat(app)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("managed metadata app directory must be a real directory")
	}
	root, err := os.OpenRoot(app)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if _, err := root.Lstat("go.mod"); err == nil {
		return nil, fmt.Errorf("managed metadata cannot use an app-root nested module")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	files, err := metadatafiles.ReadStatic(context.Background(), root.FS())
	if err != nil {
		return nil, err
	}
	var result []MetadataFile
	seen := map[string]bool{}
	for _, name := range metadatafiles.Names() {
		if data, ok := files[name]; ok {
			entry, err := metadatafiles.Validate(name, data)
			if err != nil {
				return nil, err
			}
			result = append(result, MetadataFile{Name: name, Source: "app/" + name, Digest: entry.Digest, Size: entry.Size})
			seen[name] = true
		}
	}
	entries, err := os.ReadDir(app)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		base := entry.Name()
		producer, known := metadataProducers[base]
		if !known {
			for name := range metadataProducers {
				if strings.EqualFold(name, base) {
					return nil, fmt.Errorf("managed metadata producer must use exact case %s", name)
				}
			}
			continue
		}
		if entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil, fmt.Errorf("managed metadata producer %s must be a regular file", base)
		}
		if seen[producer.name] {
			return nil, fmt.Errorf("managed metadata static and Go definitions conflict for %s", producer.name)
		}
		file, err := parseActionFile(filepath.Join(app, base))
		if err != nil {
			return nil, err
		}
		if file.Name.Name == "main" || file.Name.Name == "_" {
			return nil, fmt.Errorf("managed metadata producer must use an importable package")
		}
		if buildConstraint(file) != "!js" {
			return nil, fmt.Errorf("managed metadata producer %s requires //go:build !js", base)
		}
		found := false
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == producer.factory {
				if found {
					return nil, fmt.Errorf("duplicate managed metadata producer %s", producer.factory)
				}
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s must export typed startup function %s", base, producer.factory)
		}
		result = append(result, MetadataFile{Name: producer.name, Source: "app/" + base, Import: p.Import + "/app", Factory: producer.factory})
		seen[producer.name] = true
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	for _, file := range result {
		for _, route := range projectRoutes(p.Routes) {
			if !route.Default && !route.Missing && route.Pattern == "/"+file.Name {
				return nil, fmt.Errorf("managed metadata %s conflicts with discovered route", file.Name)
			}
		}
	}
	return result, nil
}

// CopyMetadataFiles is the build hook for a separate embedded metadata tree.
// Destination belongs to a disposable generation; no Go code is executed.
// An empty set needs no embed directive (or the caller can add an ignored marker).
func CopyMetadataFiles(ctx context.Context, p *Project, destination string) error {
	if ctx == nil {
		return fmt.Errorf("metadata copy context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Join(p.Root, "app"))
	if err != nil {
		return err
	}
	defer root.Close()
	files, err := metadatafiles.ReadStatic(ctx, root.FS())
	if err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, file := range p.MetadataFiles {
		if file.Static() {
			declared[file.Name] = true
		}
	}
	for name := range files {
		if !declared[name] {
			return fmt.Errorf("managed metadata %s appeared during build", name)
		}
	}
	for _, file := range p.MetadataFiles {
		if !file.Static() {
			continue
		}
		if !metadatafiles.IsName(file.Name) || file.Source != "app/"+file.Name {
			return fmt.Errorf("invalid managed metadata source descriptor")
		}
		data, ok := files[file.Name]
		if !ok {
			return fmt.Errorf("managed metadata %s disappeared during build", file.Name)
		}
		entry, err := metadatafiles.Validate(file.Name, data)
		if err != nil {
			return err
		}
		if entry.Digest != file.Digest || entry.Size != file.Size {
			return fmt.Errorf("managed metadata %s changed during build", file.Name)
		}
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed metadata destination must be a real directory")
	}
	for _, file := range p.MetadataFiles {
		if file.Static() {
			if _, err := os.Lstat(filepath.Join(destination, file.Name)); err == nil {
				return fmt.Errorf("managed metadata staged output collision: %s", file.Name)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	for _, file := range p.MetadataFiles {
		if !file.Static() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(destination, file.Name)
		// Do not overwrite even a regular file; this hook owns one fresh staged
		// tree, and collisions must fail before a production artifact publishes.
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := output.Write(files[file.Name])
		closeErr := output.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
