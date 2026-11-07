package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Suhaibinator/Next.go/buildassets"
)

type lambdaTarget struct{ arch string }

func newLambdaTarget(arch string) (*lambdaTarget, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("Lambda requires explicit -arch amd64 or -arch arm64")
	}
	return &lambdaTarget{arch: arch}, nil
}

func (t *lambdaTarget) environment() []string {
	return []string{"GOOS=linux", "GOARCH=" + t.arch, "CGO_ENABLED=0", "GOAMD64=v1", "GOARM64=v8.0"}
}

// CompileLambda changes only the final native link. Browser compilation and
// build-time prerender execution retain their existing target policies.
func CompileLambda(ctx context.Context, root, arch string, output io.Writer) (*Build, error) {
	target, err := newLambdaTarget(arch)
	if err != nil {
		return nil, err
	}
	return compileApplicationTarget(ctx, root, false, output, true, false, target)
}

// ValidateLambdaOutput rejects paths that could become inputs to a later build.
// Call for the final directory and the actual sibling stage before compilation.
// No application callback or asset command executes during this validation.
func ValidateLambdaOutput(ctx context.Context, root, destination string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(abs); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Lambda output cannot be a symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	// Resolve existing parents, including ordinary platform aliases such as /tmp.
	resolved, suffix := abs, []string{}
	for {
		canonical, resolveErr := filepath.EvalSymlinks(resolved)
		if resolveErr == nil {
			abs = canonical
			for i := len(suffix) - 1; i >= 0; i-- {
				abs = filepath.Join(abs, suffix[i])
			}
			break
		}
		if !os.IsNotExist(resolveErr) || filepath.Dir(resolved) == resolved {
			return "", resolveErr
		}
		suffix = append(suffix, filepath.Base(resolved))
		resolved = filepath.Dir(resolved)
	}
	if filepath.Dir(abs) == abs {
		return "", fmt.Errorf("Lambda output cannot be a filesystem root")
	}
	application := canonicalInput(root)
	if lambdaWithin(abs, application) {
		return "", fmt.Errorf("Lambda output contains the application source root")
	}
	if lambdaWithin(application, abs) {
		rel, _ := filepath.Rel(application, abs)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 || parts[0] != "dist" {
			return "", fmt.Errorf("Lambda output intersects application source/public/metadata paths")
		}
	}
	plan, err := resolveInputs(ctx, root)
	if err != nil {
		return "", err
	}
	for _, input := range plan.Files {
		if lambdaWithin(abs, canonicalInput(input)) {
			return "", fmt.Errorf("Lambda output contains a selected input file")
		}
	}
	for _, backing := range plan.OverlaySources {
		if backing != "" && lambdaWithin(abs, canonicalInput(backing)) {
			return "", fmt.Errorf("Lambda output contains caller overlay backing source")
		}
	}
	protectedData, err := goInputJSON(ctx, root, "env", "-json", "GOROOT", "GOMODCACHE", "GOCACHE")
	if err != nil {
		return "", err
	}
	var protected map[string]string
	if err := json.Unmarshal(protectedData, &protected); err != nil {
		return "", err
	}
	for _, directory := range protected {
		if directory != "" && directory != "off" {
			directory = canonicalInput(directory)
			if lambdaWithin(directory, abs) || lambdaWithin(abs, directory) {
				return "", fmt.Errorf("Lambda output intersects compiler or dependency cache inputs")
			}
		}
	}
	for _, selected := range plan.Roots {
		selected = canonicalInput(selected)
		if lambdaWithin(abs, selected) {
			return "", fmt.Errorf("Lambda output contains a selected source root")
		}
		if !lambdaWithin(selected, abs) {
			continue
		}
		rel, _ := filepath.Rel(selected, abs)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 || parts[0] != "dist" {
			return "", fmt.Errorf("Lambda output inside a selected module must be below its excluded dist directory")
		}
		for _, part := range parts {
			if part == "public" {
				return "", fmt.Errorf("Lambda output intersects public inputs")
			}
		}
		// Embed and asset declarations cannot escape their containing directory.
		// Only ancestors of the future output can capture it. Include declarations
		// below dist: an explicitly imported dist package may itself use go:embed.
		for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
			if err := lambdaCheckDeclarations(ctx, dir, abs, plan); err != nil {
				return "", err
			}
			if dir == selected {
				break
			}
		}
	}
	return abs, ctx.Err()
}

func lambdaWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func lambdaCheckDeclarations(ctx context.Context, dir, destination string, plan inputPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pipeline, present, err := buildassets.Load(dir)
	if err != nil {
		return err
	}
	if present {
		for _, tool := range pipeline.Tools {
			for _, input := range tool.Inputs {
				name := filepath.Join(dir, filepath.FromSlash(input))
				if lambdaWithin(name, destination) || lambdaWithin(destination, name) {
					return fmt.Errorf("Lambda output intersects a declared asset input")
				}
			}
		}
	}
	folder, err := os.Open(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if folder != nil {
		defer folder.Close()
	}
	count := 0
	for folder != nil {
		entries, readErr := folder.ReadDir(128)
		for _, entry := range entries {
			count++
			if count > 10000 {
				return fmt.Errorf("Lambda output declaration scan exceeds entry limit")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if filepath.Ext(entry.Name()) != ".go" || entry.IsDir() {
				continue
			}
			name := filepath.Join(dir, entry.Name())
			backing := name
			if replacement, ok := overlayBacking(plan.Overlays, name); ok {
				backing = replacement
			}
			if backing == "" {
				continue
			}
			if err := lambdaCheckGoEmbed(ctx, name, backing, destination); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	// Caller overlays can introduce a logical source file absent on disk.
	for name, backing := range plan.OverlaySources {
		if filepath.Dir(name) == dir && backing != "" {
			if err := lambdaCheckGoEmbed(ctx, name, backing, destination); err != nil {
				return err
			}
		}
	}
	return nil
}

func lambdaCheckGoEmbed(ctx context.Context, name, backing, destination string) error {
	file, err := os.Open(backing)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(inputReader{ctx, io.LimitReader(file, maxInputFile+1)})
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > maxInputFile {
		return fmt.Errorf("Lambda declaration scan exceeds Go source limit")
	}
	for _, pattern := range embeddedPatterns(name, data) {
		pattern = strings.TrimPrefix(pattern, "all:")
		for _, future := range []string{destination, filepath.Join(destination, "function.zip"), filepath.Join(destination, "deployment.json")} {
			rel, err := filepath.Rel(filepath.Dir(name), future)
			if err != nil || !lambdaWithin(filepath.Dir(name), future) {
				continue
			}
			for rel = filepath.ToSlash(rel); rel != "."; rel = path.Dir(rel) {
				matched, matchErr := path.Match(pattern, rel)
				if matchErr != nil || matched {
					return fmt.Errorf("Lambda output intersects a declared Go embed pattern")
				}
			}
		}
	}
	return nil
}
