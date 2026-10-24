// Package scaffold creates an independent application without overwriting an
// existing destination. The framework path is explicit while APIs are unreleased.
package scaffold

import (
	"context"
	"embed"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed starter
var starter embed.FS

const framework = "github.com/Suhaibinator/Next.go"

type Options struct{ Directory, Module, Framework string }

func Create(ctx context.Context, options Options, output io.Writer) error {
	if options.Directory == "" || options.Module == "" || options.Framework == "" {
		return fmt.Errorf("init requires a destination, -module, and -framework source path")
	}
	if options.Module == framework {
		return fmt.Errorf("application module must differ from the framework module")
	}
	destination, err := filepath.Abs(options.Directory)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination already exists: %s; choose a new directory", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	frameworkRoot, err := filepath.Abs(options.Framework)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(frameworkRoot, "go.mod")); err != nil {
		return fmt.Errorf("framework directory needs its own go.mod: %w", err)
	}
	inspect := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Path}}")
	inspect.Dir = frameworkRoot
	inspect.Env = append(inspect.Environ(), "GOWORK=off")
	module, err := inspect.Output()
	if err != nil {
		return fmt.Errorf("inspect framework module: %w", err)
	}
	if strings.TrimSpace(string(module)) != framework {
		return fmt.Errorf("framework source must declare module %s", framework)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".nextgo-init-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	run := func(args ...string) error {
		command := exec.CommandContext(ctx, "go", args...)
		command.Dir = stage
		command.Env = append(command.Environ(), "GOWORK=off")
		command.Stdout = output
		command.Stderr = output
		if err := command.Run(); err != nil {
			return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	// Let the Go tool validate module syntax; no shell or dependency download runs.
	if err := run("mod", "init", options.Module); err != nil {
		return err
	}
	if err := run("mod", "edit", "-go=1.24", "-require="+framework+"@v0.0.0", "-replace="+framework+"="+frameworkRoot); err != nil {
		return err
	}
	err = fs.WalkDir(starter, "starter", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel("starter", name)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = strings.TrimSuffix(relative, ".tmpl")
		if relative == "gitignore" {
			relative = ".gitignore"
		}
		target := filepath.Join(stage, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := starter.ReadFile(name)
		if err != nil {
			return err
		}
		data = []byte(strings.ReplaceAll(string(data), "{{COUNTER_IMPORT}}", strconv.Quote(options.Module+"/components/counter")))
		if strings.HasSuffix(target, ".go") {
			data, err = format.Source(data)
			if err != nil {
				return fmt.Errorf("format starter %s: %w", relative, err)
			}
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A completed staging tree is published only to a new destination. Existing
	// nonempty directories/files cannot be replaced by directory rename either.
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination appeared during initialization: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, destination); err != nil {
		return fmt.Errorf("publish starter: %w", err)
	}
	fmt.Fprintf(output, "Created %s\nFrom that directory run:\n  go run %s/cmd/nextgo dev .\n", destination, framework)
	return nil
}
