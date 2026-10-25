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

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const maxLazyComponents = 8

func lazyComponent(dir string) (bool, error) {
	config, err := readComponentConfig(dir)
	return config.Loading == "lazy", err
}

// ComponentRefresh is an explicit, versioned contract for development-only state
// and DOM compatibility. Merely being lazy never opts a component into refresh.
type ComponentRefresh struct {
	Version     int    `json:"version"`
	StateSchema string `json:"stateSchema"`
	DOMSchema   string `json:"domSchema"`
}

type componentConfig struct {
	Loading string            `json:"loading"`
	Refresh *ComponentRefresh `json:"refresh,omitempty"`
}

func readComponentConfig(dir string) (componentConfig, error) {
	var config componentConfig
	path := filepath.Join(dir, "component.json")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil {
		return config, err
	}
	if len(data) > 1024 {
		return config, fmt.Errorf("component.json exceeds 1 KiB")
	}
	if err := wirejson.Unique(data); err != nil {
		return config, fmt.Errorf("invalid component.json: %w", err)
	}
	if err := wirejson.Strict(data, &config); err != nil {
		return config, fmt.Errorf("invalid component.json: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return config, fmt.Errorf("component.json must be an object")
	}
	for name, value := range fields {
		if name != "loading" && name != "refresh" || strings.TrimSpace(string(value)) == "null" {
			return config, fmt.Errorf("invalid component.json field")
		}
	}
	if config.Loading != "lazy" && config.Loading != "eager" {
		return config, fmt.Errorf("component loading must be eager or lazy")
	}
	if config.Refresh != nil {
		var refresh map[string]json.RawMessage
		if err := json.Unmarshal(fields["refresh"], &refresh); err != nil || len(refresh) != 3 {
			return config, fmt.Errorf("refresh requires version, stateSchema and domSchema")
		}
		for _, field := range []string{"version", "stateSchema", "domSchema"} {
			value, ok := refresh[field]
			if !ok || strings.TrimSpace(string(value)) == "null" {
				return config, fmt.Errorf("invalid refresh schema field")
			}
		}
		if config.Loading != "lazy" || config.Refresh.Version != devrefresh.Version || !devrefresh.ValidToken(config.Refresh.StateSchema) || !devrefresh.ValidToken(config.Refresh.DOMSchema) {
			return config, fmt.Errorf("refresh requires lazy loading, version 1 and bounded schema tokens")
		}
	}
	return config, nil
}

type lazyOutput struct {
	Name, File, Digest, Epoch string
	StateSchema, DOMSchema    string
	Size                      int
	Data                      []byte
}

func compileLazy(ctx context.Context, p *Project, buildDir, epoch, tagsFlag string, output io.Writer) ([]lazyOutput, error) {
	return compileLazyRefresh(ctx, p, buildDir, epoch, tagsFlag, output, nil)
}

func lazyMainSource(component Component, epoch string, refresh bool) string {
	if refresh && component.Refresh != nil {
		return fmt.Sprintf("package main\nimport(\"%s/client\";component %q)\nfunc main(){client.Register(%q,component.Mount);<-client.RunModuleRefresh(%q,%q,%q,%q)}\n", framework, component.Import, component.Name, component.Name, epoch, component.Refresh.StateSchema, component.Refresh.DOMSchema)
	}
	return fmt.Sprintf("package main\nimport(\"%s/client\";component %q)\nfunc main(){client.Register(%q,component.Mount);client.RunModule(%q,%q);select{}}\n", framework, component.Import, component.Name, component.Name, epoch)
}

func compileLazyRefresh(ctx context.Context, p *Project, buildDir, epoch, tagsFlag string, output io.Writer, refresh *devRefreshPlan) ([]lazyOutput, error) {
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
		componentEpoch := epoch
		if refresh != nil {
			componentEpoch = refresh.epochs[component.Name]
		}
		source := lazyMainSource(component, componentEpoch, refresh != nil)
		if err := writeGo(filepath.Join(dir, "main.go"), source); err != nil {
			return nil, err
		}
		if err := validateBrowserGraph(ctx, p, dir, tagsFlag); err != nil {
			return nil, err
		}
		target, overlay, err := generatedEntryOverlay(ctx, p.Root, dir, fmt.Sprintf("module-%d", i))
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "module.wasm")
		if err := run(ctx, p.Root, output, []string{"GOOS=js", "GOARCH=wasm"}, "build", tagsFlag, "-overlay="+overlay, "-trimpath", "-ldflags=-s -w", "-o", path, target); err != nil {
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
		module := lazyOutput{Name: component.Name, File: "module-" + digest + ".wasm", Digest: digest, Epoch: componentEpoch, Size: len(data), Data: data}
		if refresh != nil && component.Refresh != nil {
			module.StateSchema, module.DOMSchema = component.Refresh.StateSchema, component.Refresh.DOMSchema
		}
		modules = append(modules, module)
	}
	return modules, nil
}
func lazyIdentity(modules []lazyOutput) string {
	var b strings.Builder
	for _, module := range modules {
		data, _ := json.Marshal(struct {
			Name, File, Digest, Epoch string
			Size                      int
			StateSchema               string `json:",omitempty"`
			DOMSchema                 string `json:",omitempty"`
		}{module.Name, module.File, module.Digest, module.Epoch, module.Size, module.StateSchema, module.DOMSchema})
		fmt.Fprintf(&b, "%d:%s", len(data), data)
	}
	return b.String()
}
