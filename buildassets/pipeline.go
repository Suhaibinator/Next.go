//go:build !js

// Package buildassets runs optional, explicitly configured asset build tools.
// It is a build-time API and is deliberately unavailable to browser packages.
package buildassets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const ManifestName = "nextgo.assets.json"

const (
	maxManifest = 1 << 20
	maxFile     = 64 << 20
	maxTotal    = 256 << 20
	maxFiles    = 10000
	maxLog      = 64 << 10
)

// Pipeline describes build tools. Version 1 tools are independent; version 2
// permits explicit dependency snapshots and nonpublished intermediates. Outputs
// are exact file paths; directories/globs are not outputs. Inputs may be directories.
type Pipeline struct {
	Version int    `json:"version"`
	Tools   []Tool `json:"tools"`
}

type Tool struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command,omitempty"`
	Inputs         []string          `json:"inputs"`
	Outputs        []string          `json:"outputs"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	// Version 2 only. Aliases name isolated snapshots of declared predecessors.
	Dependencies map[string]string `json:"dependencies,omitempty"`
	// Intermediate verifies outputs without adding them to the public result.
	Intermediate bool `json:"intermediate,omitempty"`
	// Transform is an alternative to Command for Go callers. It must cooperate
	// with context cancellation. JSON manifests only support Command.
	Transform func(context.Context, Workspace) error `json:"-"`
}

// Workspace holds private input snapshots and an initially empty output tree.
// External tools receive these directories through NEXTGO_ASSET_* environment
// variables and {inputs}, {outputs}, {project} argv substitutions.
type Workspace struct{ Project, Inputs, Outputs string }

// Dependencies names version 2's private dependency snapshot directory. Only
// explicitly declared aliases exist beneath it. Version 1 does not create it.
// A method preserves the existing three-field Workspace struct shape.
func (w Workspace) Dependencies() string {
	return filepath.Join(filepath.Dir(w.Inputs), "dependencies")
}

// WorkspaceFromEnv lets a small Go executable implement a typed asset hook.
func WorkspaceFromEnv() (Workspace, error) {
	w := Workspace{os.Getenv("NEXTGO_ASSET_PROJECT_DIR"), os.Getenv("NEXTGO_ASSET_INPUT_DIR"), os.Getenv("NEXTGO_ASSET_OUTPUT_DIR")}
	if w.Project == "" || w.Inputs == "" || w.Outputs == "" {
		return Workspace{}, fmt.Errorf("asset workspace environment is missing")
	}
	return w, nil
}

type Options struct {
	Root string
	// Stage must not exist. Its parent must be a private build directory.
	Stage string
	Log   io.Writer
}

type Result struct{ Dir, Digest string }

// Load reads the optional manifest. A missing manifest returns present=false.
func Load(root string) (pipeline Pipeline, present bool, err error) {
	data, present, err := manifest(root)
	if err != nil || !present {
		return pipeline, present, err
	}
	if err = wirejson.Unique(data); err != nil {
		return pipeline, true, fmt.Errorf("%s: duplicate or malformed JSON keys", ManifestName)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&pipeline); err != nil {
		return pipeline, true, fmt.Errorf("%s: %w", ManifestName, err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return pipeline, true, fmt.Errorf("%s: expected one JSON object", ManifestName)
	}
	return pipeline, true, validate(pipeline)
}

func manifest(root string) ([]byte, bool, error) {
	name := filepath.Join(root, ManifestName)
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifest {
		return nil, true, fmt.Errorf("%s must be a regular file of at most 1 MiB", ManifestName)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, true, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, true, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, true, fmt.Errorf("%s changed while opening", ManifestName)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxManifest+1))
	if len(data) > maxManifest {
		return nil, true, fmt.Errorf("%s exceeds 1 MiB", ManifestName)
	}
	return data, true, err
}

func validate(p Pipeline) error {
	if p.Version != 1 && p.Version != 2 {
		return fmt.Errorf("asset pipeline version must be 1 or 2")
	}
	if len(p.Tools) > 64 {
		return fmt.Errorf("asset pipeline exceeds 64 tools")
	}
	names, outputs, directories := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, t := range p.Tools {
		if t.Name == "" || len(t.Name) > 128 || strings.ContainsAny(t.Name, "\x00\r\n") || names[t.Name] {
			return fmt.Errorf("asset tool names must be nonempty and unique")
		}
		names[t.Name] = true
		if (len(t.Command) == 0) == (t.Transform == nil) {
			return fmt.Errorf("asset tool %q needs exactly one command or transform", t.Name)
		}
		if len(t.Command) > 256 || (len(t.Command) > 0 && t.Command[0] == "") {
			return fmt.Errorf("invalid asset command for %q", t.Name)
		}
		for _, arg := range t.Command {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("asset command contains NUL")
			}
		}
		if t.TimeoutSeconds < 0 || t.TimeoutSeconds > 600 {
			return fmt.Errorf("asset timeout must be between 0 and 600 seconds")
		}
		if len(t.Inputs) > 256 || len(t.Outputs) == 0 || len(t.Outputs) > 256 {
			return fmt.Errorf("asset tool %q needs 1–256 outputs and at most 256 inputs", t.Name)
		}
		for _, input := range t.Inputs {
			if err := safePath(input, false); err != nil {
				return fmt.Errorf("asset input: %w", err)
			}
		}
		if p.Version == 1 && (t.Intermediate || len(t.Dependencies) != 0) {
			return fmt.Errorf("asset tool dependencies and intermediates require version 2")
		}
		localOutputs, localDirectories := map[string]bool{}, map[string]string{}
		for _, output := range t.Outputs {
			if err := safePath(output, true); err != nil {
				return fmt.Errorf("asset output: %w", err)
			}
			if err := declaredOutput(output, localOutputs, localDirectories); err != nil {
				return err
			}
			if !t.Intermediate {
				if err := declaredOutput(output, outputs, directories); err != nil {
					return err
				}
			}
		}
		for key, value := range t.Env {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) || strings.HasPrefix(strings.ToUpper(key), "NEXTGO_ASSET_") {
				return fmt.Errorf("invalid asset tool environment key %q", key)
			}
		}
	}
	if p.Version == 2 {
		_, err := dependencyOrder(p)
		return err
	}
	return nil
}

func safePath(name string, output bool) error {
	if name == "" || name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00\r\n") {
		return fmt.Errorf("path must be a clean relative slash path: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if secretName(lower) || (output && strings.HasPrefix(lower, ".")) {
			return fmt.Errorf("secret or private file cannot be an asset: %q", name)
		}
	}
	if output && strings.EqualFold(strings.Split(name, "/")[0], "_nextgo") {
		return fmt.Errorf("reserved asset path: %q", name)
	}
	return nil
}

func secretName(name string) bool {
	for _, private := range []string{".git", ".aws", ".ssh", ".azure", ".kube", ".gnupg", ".nextgo", ".npmrc", ".netrc", ".pypirc", "credentials", "secrets", "id_rsa", "id_ed25519", "id_dsa", "id_ecdsa"} {
		if name == private {
			return true
		}
	}
	if name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasPrefix(name, "credentials.") || strings.HasPrefix(name, "secrets.") {
		return true
	}
	for _, extension := range []string{".pem", ".key", ".p12", ".pfx", ".env"} {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

// Run creates a new generation without changing root/public or any input.
// Only a completely successful result is returned. Failed stages are removed.
// Tools are trusted build code; executing them is not an OS security sandbox.
func Run(ctx context.Context, p Pipeline, options Options) (_ *Result, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validate(p); err != nil {
		return nil, err
	}
	if p.Version == 2 {
		p = cloneGraphPipeline(p)
	}

	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	stage, err := filepath.Abs(options.Stage)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(stage)
		}
	}()
	public := filepath.Join(stage, "public")
	if err = os.Mkdir(public, 0755); err != nil {
		return nil, err
	}
	order := make([]int, len(p.Tools))
	for i := range order {
		order[i] = i
	}
	if p.Version == 2 {
		order, err = dependencyOrder(p)
		if err != nil {
			return nil, err
		}
	}
	var verified map[int]string
	var graphQuota *quota
	if p.Version == 2 {
		verified = make(map[int]string)
		graphQuota = &quota{}
	}
	for _, i := range order {
		tool := p.Tools[i]
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		work := filepath.Join(stage, fmt.Sprintf("tool-%03d", i))
		w := Workspace{root, filepath.Join(work, "inputs"), filepath.Join(work, "outputs")}
		directories := []string{w.Inputs, w.Outputs}
		if p.Version == 2 {
			directories = append(directories, w.Dependencies())
		}
		for _, dir := range directories {
			if err = os.MkdirAll(dir, 0700); err != nil {
				return nil, err
			}
		}
		if err = snapshot(ctx, root, w.Inputs, tool.Inputs); err != nil {
			return nil, fmt.Errorf("asset tool %q inputs: %w", tool.Name, err)
		}
		if p.Version == 2 {
			if err = snapshotDependencies(ctx, p, tool, verified, w.Dependencies(), graphQuota); err != nil {
				return nil, fmt.Errorf("asset tool %q dependencies: %w", tool.Name, err)
			}
		}
		seconds := tool.TimeoutSeconds
		if seconds == 0 {
			seconds = 120
		}
		toolCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		if tool.Transform != nil {
			err = tool.Transform(toolCtx, w)
		} else {
			err = command(toolCtx, tool, w, work, options.Log, p.Version == 2)
		}
		if err == nil {
			err = toolCtx.Err()
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("asset tool %q: %w", tool.Name, err)
		}
		if p.Version == 1 {
			if err = collect(ctx, w.Outputs, public, tool.Outputs); err != nil {
				return nil, fmt.Errorf("asset tool %q outputs: %w", tool.Name, err)
			}
			continue
		}
		canonical := filepath.Join(work, "verified")
		if err = os.Mkdir(canonical, 0700); err != nil {
			return nil, err
		}
		if err = collectWithQuota(ctx, w.Outputs, canonical, tool.Outputs, graphQuota); err != nil {
			return nil, fmt.Errorf("asset tool %q outputs: %w", tool.Name, err)
		}
		verified[i] = canonical
		if !tool.Intermediate {
			if err = collectWithQuota(ctx, canonical, public, tool.Outputs, graphQuota); err != nil {
				return nil, fmt.Errorf("asset tool %q publication: %w", tool.Name, err)
			}
		}
	}
	digest, err := digestTree(ctx, public)
	if err != nil {
		return nil, err
	}
	success = true
	return &Result{Dir: public, Digest: digest}, nil
}

func command(ctx context.Context, tool Tool, w Workspace, dir string, log io.Writer, graph bool) error {
	pairs := []string{"{inputs}", w.Inputs, "{outputs}", w.Outputs, "{project}", w.Project}
	if graph {
		pairs = append(pairs, "{dependencies}", w.Dependencies())
	}
	replace := strings.NewReplacer(pairs...)
	args := make([]string, len(tool.Command))
	for i, arg := range tool.Command {
		args[i] = replace.Replace(arg)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir, cmd.WaitDelay = dir, time.Second
	cmd.Env = cmd.Environ()
	env := make(map[string]string, len(tool.Env)+3)
	for key, value := range tool.Env {
		env[key] = replace.Replace(value)
	}
	env["NEXTGO_ASSET_PROJECT_DIR"], env["NEXTGO_ASSET_INPUT_DIR"], env["NEXTGO_ASSET_OUTPUT_DIR"] = w.Project, w.Inputs, w.Outputs
	if graph {
		env["NEXTGO_ASSET_DEPENDENCY_DIR"] = w.Dependencies()
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		filtered := cmd.Env[:0]
		for _, old := range cmd.Env {
			if !strings.HasPrefix(old, key+"=") {
				filtered = append(filtered, old)
			}
		}
		cmd.Env = append(filtered, key+"="+env[key])
	}
	bounded := &boundedLog{destination: log, remaining: maxLog}
	cmd.Stdout, cmd.Stderr = bounded, bounded
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}

type boundedLog struct {
	mu          sync.Mutex
	destination io.Writer
	remaining   int
	truncated   bool
}

func (b *boundedLog) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	if b.destination == nil {
		return n, nil
	}
	if len(data) > b.remaining {
		data = data[:b.remaining]
	}
	if len(data) > 0 {
		_, _ = b.destination.Write(data)
		b.remaining -= len(data)
	}
	if len(data) < n && !b.truncated {
		_, _ = io.WriteString(b.destination, "\n[asset tool log truncated after 64 KiB]\n")
		b.truncated = true
	}
	return n, nil
}
