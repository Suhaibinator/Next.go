package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// Ask the compiler for the actual target-specific transitive graph, including
// user build flags and indirect dependencies. Filename scanning alone cannot
// establish whether a server package reaches the browser.
func validateBrowserGraph(ctx context.Context, p *Project, entry string, buildFlags ...string) error {
	canonical, canonicalErr := filepath.EvalSymlinks(entry)
	if canonicalErr != nil {
		return canonicalErr
	}
	entry = canonical
	args := append([]string{"list", "-deps", "-json"}, buildFlags...)
	command := exec.CommandContext(ctx, "go", append(args, entry)...)
	command.Dir = p.Root
	command.Env = append(command.Environ(), "GOOS=js", "GOARCH=wasm")
	output, err := command.Output()
	if err != nil {
		detail := err.Error()
		if failure, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(failure.Stderr))
		}
		return fmt.Errorf("browser dependency graph: %s", detail)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var dependency struct {
			ImportPath string
			GoFiles    []string
		}
		err := decoder.Decode(&dependency)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read browser dependency graph: %w", err)
		}
		if dependency.ImportPath == p.Import+"/app" || strings.HasPrefix(dependency.ImportPath, p.Import+"/app/") {
			return fmt.Errorf("browser dependency %s imports application route code; move shared values outside app/ and mark server-only packages with a blank serveronly import", dependency.ImportPath)
		}
		if strings.HasPrefix(dependency.ImportPath, p.Import+"/components/") {
			for _, file := range dependency.GoFiles {
				if file == "server.go" {
					return fmt.Errorf("browser dependency %s includes server.go; add //go:build !js to its server implementation and keep shared props in another file", dependency.ImportPath)
				}
			}
		}
	}
}
