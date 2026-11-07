package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

func readExportConfig(ctx context.Context, binary, build, domain string) (nextgo.ExportConfig, error) {
	return readExportConfigEnv(ctx, binary, build, domain, nil)
}
func readExportConfigEnv(ctx context.Context, binary, build, domain string, env []string) (nextgo.ExportConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, diagnostic := &cappedOutput{limit: 128 << 10}, &cappedOutput{limit: 64 << 10}
	command := exec.CommandContext(ctx, binary, "-export-config", "-export-domain", domain)
	command.Stdout, command.Stderr, command.WaitDelay = output, diagnostic, time.Second
	command.Env = env
	var config nextgo.ExportConfig
	if err := command.Run(); err != nil {
		return config, fmt.Errorf("read static export configuration: %w: %s", err, diagnostic.String())
	}
	if output.overflow {
		return config, fmt.Errorf("static export configuration exceeds 128 KiB")
	}
	return decodeExportConfig(output.Bytes(), build, domain)
}

func decodeExportConfig(data []byte, build, domain string) (nextgo.ExportConfig, error) {
	var config nextgo.ExportConfig
	// Versions 1/2 keep their original encoding/json extension behavior.
	// Strict and duplicate-free shapes belong to the new version 3 contract.
	if len(data) == 0 || len(data) > 128<<10 || json.Unmarshal(data, &config) != nil {
		return config, fmt.Errorf("invalid static export configuration; write application build logs to stderr")
	}
	if config.Build != build || config.Version < 1 || config.Version > 3 || config.Version == 1 && len(config.Metadata) > 0 || config.Version == 2 && len(config.Metadata) == 0 || config.Version != 3 && config.StaticShell != nil {
		return config, fmt.Errorf("incompatible static export configuration")
	}
	if config.Version == 3 {
		if !utf8.Valid(data) || wirejson.Unique(data) != nil || wirejson.Strict(data, &config) != nil {
			return config, fmt.Errorf("invalid version 3 static export configuration")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return config, fmt.Errorf("invalid static export configuration object")
		}
		for _, field := range []string{"version", "build", "paths", "scopes", "staticShell"} {
			if len(fields[field]) == 0 || bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) {
				return config, fmt.Errorf("invalid static export configuration field %s", field)
			}
		}
		if config.StaticShell == nil {
			return config, fmt.Errorf("version 3 requires one static shell declaration")
		}
	}
	if err := validateStaticShellExportDeclaration(config.StaticShell, config.Paths, config.Scopes, config.Domain); err != nil {
		return config, err
	}
	if err := validateMetadataInventory(config.Paths, config.Metadata); err != nil {
		return config, err
	}
	expected := ""
	if domain != "" {
		var err error
		expected, err = paths.CanonicalHost(domain)
		if err != nil {
			return config, err
		}
	}
	if config.Domain != expected && !defaultPortAlias(expected, config.Domain) {
		return config, fmt.Errorf("static export declaration selected a different domain")
	}
	if err := validateExportDomain(config.Domain); err != nil {
		return config, err
	}
	return config, validateExportScopes(config.Paths, config.Scopes)
}

// Native configuration validates the selected scheme/authority. Its declaration
// may omit a configured scheme's default port, matching browser URL behavior.
func defaultPortAlias(selected, canonical string) bool {
	if selected == "" || canonical == "" {
		return false
	}
	u := &url.URL{Host: selected}
	if u.Port() != "80" && u.Port() != "443" {
		return false
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host == canonical
}

func validateExportDomain(domain string) error {
	if domain == "" {
		return nil
	}
	canonical, err := paths.CanonicalHost(domain)
	if err != nil || canonical != domain {
		return fmt.Errorf("invalid canonical static export domain")
	}
	return nil
}

func validateExportScopes(root nextgo.PathConfig, scopes []nextgo.StaticExportScope) error {
	if err := root.Validate(); err != nil {
		return err
	}
	if len(scopes) == 0 || len(scopes) > 32 {
		return fmt.Errorf("static export requires 1..32 ownership scopes")
	}
	seen := map[string]bool{}
	identities := map[string]bool{}
	for _, scope := range scopes {
		if scope.Locale == "" && (len(scopes) != 1 || scope.Prefix != "") || scope.Locale != "" && (strings.Contains(scope.Locale, "/") || scope.Prefix != "" && scope.Prefix != "/"+scope.Locale) {
			return fmt.Errorf("static export scopes must declare a single app or distinct locale prefixes")
		}
		child := root
		child.BasePath += scope.Prefix
		if err := child.Validate(); err != nil {
			return err
		}
		if err := (nextgo.PathConfig{BasePath: scope.Prefix}).Validate(); err != nil {
			return err
		}
		if seen[scope.Prefix] || identities[scope.Configuration] || !navigation.ValidConfigurationID(scope.Configuration) || len(scope.Locale) > 63 {
			return fmt.Errorf("invalid or duplicate static export ownership scope")
		}
		seen[scope.Prefix], identities[scope.Configuration] = true, true
	}
	return nil
}

// Choose the most specific declared mount. An unprefixed default locale can
// coexist with /fr; its catch-all route must never take ownership of /fr data.
func exportScope(scopes []nextgo.StaticExportScope, canonical string) (nextgo.StaticExportScope, string, bool) {
	best := -1
	var selected nextgo.StaticExportScope
	local := ""
	for _, scope := range scopes {
		if remainder, ok := paths.Strip(scope.Prefix, canonical); ok && len(scope.Prefix) > best {
			best, selected, local = len(scope.Prefix), scope, remainder
		}
	}
	return selected, local, best >= 0
}

func scopedExportPath(prefix, local string) string {
	if local == "/" && prefix != "" {
		return prefix
	}
	return prefix + local
}

func validateScopedExportPaths(p *project.Project, candidates []string, config nextgo.ExportConfig) ([]string, error) {
	if err := validateExportScopes(config.Paths, config.Scopes); err != nil {
		return nil, err
	}
	if len(candidates) > 10000 {
		return nil, fmt.Errorf("static export exceeds 10000 paths")
	}
	seen, files := map[string]bool{}, map[string]string{}
	result := []string{}
	total := 0
	for _, candidate := range candidates {
		if config.Paths.TrailingSlash && candidate != "/" {
			candidate = strings.TrimSuffix(candidate, "/")
		}
		canonical, decoded, err := exportPath(candidate)
		if err != nil {
			return nil, err
		}
		scope, local, ok := exportScope(config.Scopes, canonical)
		if !ok {
			return nil, fmt.Errorf("static export path %q has no declared ownership scope", canonical)
		}
		if _, err := validateExportPaths(p, []string{local}); err != nil {
			return nil, err
		}
		if seen[canonical] {
			continue
		}
		dataPath := scopedExportDataPath(scope.Prefix, decoded)
		if previous, exists := files[dataPath]; exists {
			return nil, fmt.Errorf("export paths %q and %q produce the same navigation file", previous, canonical)
		}
		files[dataPath], seen[canonical] = canonical, true
		total += len(canonical)
		if len(canonical) > 8192 || total > 16<<20 {
			return nil, fmt.Errorf("static export paths exceed URL bounds")
		}
		result = append(result, canonical)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no static pages to export; provide concrete dynamic paths with -paths")
	}
	sort.Strings(result)
	return result, nil
}

func scopedExportDataPath(prefix, decoded string) string {
	local, _ := paths.Strip(prefix, decoded) // caller already selected this scope
	return filepath.Join(filepath.FromSlash(strings.TrimPrefix(prefix, "/")), exportDataPath(local))
}

// -paths remains logical application paths, expanded once for each locale.
func expandExportPaths(selected []string, scopes []nextgo.StaticExportScope) ([]string, error) {
	if len(scopes) == 0 || len(scopes) > 32 || len(selected) > 10000/len(scopes) {
		return nil, fmt.Errorf("static export exceeds 10000 paths")
	}
	result := make([]string, 0, len(selected)*len(scopes))
	for _, scope := range scopes {
		for _, local := range selected {
			result = append(result, scopedExportPath(scope.Prefix, local))
		}
	}
	return result, nil
}

func validateStaticShellExportDeclaration(shell *nextgo.StaticShellDeclaration, deployment nextgo.PathConfig, scopes []nextgo.StaticExportScope, domain string) error {
	if shell == nil {
		return nil
	}
	seed, err := navigation.ValidateStaticShellDeclaration(shell.Pattern, shell.Component)
	if err != nil || shell.Version != 1 || shell.SeedPath != seed {
		return fmt.Errorf("invalid static shell declaration")
	}
	if domain != "" || len(scopes) != 1 || scopes[0].Prefix != "" || scopes[0].Locale != "" {
		return fmt.Errorf("static shell v1 requires one unlocalized ownership scope")
	}
	return validateExportScopes(deployment, scopes)
}
