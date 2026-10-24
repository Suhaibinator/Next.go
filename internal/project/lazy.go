package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const maxLazyComponents = 8

func lazyComponent(dir string) (bool, error) {
	path := filepath.Join(dir, "component.json")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil {
		return false, err
	}
	if len(data) > 1024 {
		return false, fmt.Errorf("component.json exceeds 1 KiB")
	}
	if err := wirejson.Unique(data); err != nil {
		return false, fmt.Errorf("invalid component.json: %w", err)
	}
	var config struct {
		Loading string `json:"loading"`
	}
	if err := wirejson.Strict(data, &config); err != nil {
		return false, fmt.Errorf("invalid component.json: %w", err)
	}
	if config.Loading != "lazy" && config.Loading != "eager" {
		return false, fmt.Errorf("component loading must be eager or lazy")
	}
	return config.Loading == "lazy", nil
}

type lazyOutput struct {
	Name, File, Digest, Epoch string
	Size                      int
	Data                      []byte
}

func compileLazy(ctx context.Context, p *Project, buildDir, epoch, tagsFlag string, output io.Writer) ([]lazyOutput, error) {
	var modules []lazyOutput
	for i, component := range p.Components {
		if !component.Lazy {
			continue
		}
		if len(modules) >= maxLazyComponents {
			return nil, fmt.Errorf("at most eight lazy components are supported")
		}
		dir := filepath.Join(buildDir, fmt.Sprintf("module-%d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		source := fmt.Sprintf("package main\nimport(\"%s/client\";component %q)\nfunc main(){client.Register(%q,component.Mount);client.RunModule(%q,%q);select{}}\n", framework, component.Import, component.Name, component.Name, epoch)
		if err := writeGo(filepath.Join(dir, "main.go"), source); err != nil {
			return nil, err
		}
		if err := validateBrowserGraph(ctx, p, dir, tagsFlag); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "module.wasm")
		if err := run(ctx, p.Root, output, []string{"GOOS=js", "GOARCH=wasm"}, "build", tagsFlag, "-trimpath", "-ldflags=-s -w", "-o", path, dir); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(data) > 8<<20 {
			return nil, fmt.Errorf("lazy component %s exceeds 8 MiB", component.Name)
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		modules = append(modules, lazyOutput{Name: component.Name, File: "module-" + digest + ".wasm", Digest: digest, Epoch: epoch, Size: len(data), Data: data})
	}
	return modules, nil
}
func lazyIdentity(modules []lazyOutput) string {
	var b strings.Builder
	for _, module := range modules {
		data, _ := json.Marshal(struct {
			Name, File, Digest, Epoch string
			Size                      int
		}{module.Name, module.File, module.Digest, module.Epoch, module.Size})
		fmt.Fprintf(&b, "%d:%s", len(data), data)
	}
	return b.String()
}
