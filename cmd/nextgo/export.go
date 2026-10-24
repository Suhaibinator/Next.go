package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	htmlstd "html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/environment"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/routing"
)

func executeExport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	output := flags.String("out", "dist/static", "static output directory")
	selected := flags.String("paths", "", "comma-separated concrete page paths; defaults to static pages plus StaticParams results")
	domain := flags.String("domain", "", "configured locale host for a separate domain-specific static artifact")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("provide one application directory; flags precede the directory")
	}
	root := flags.Arg(0)
	p, err := project.Discover(root)
	if err != nil {
		return err
	}
	var paths []string
	if *selected != "" {
		paths, err = exportPaths(p, *selected)
		if err != nil {
			return err
		}
	}
	if err := validateExportDestination(*output); err != nil {
		return err
	}
	build, err := project.Compile(ctx, root, false, os.Stdout)
	if err != nil {
		return err
	}
	defer os.RemoveAll(build.Dir)
	exportEnv, err := environment.Load(p.Root, environment.Production, append(os.Environ(), "AWS_LAMBDA_RUNTIME_API="))
	if err != nil {
		return err
	}
	exportEnv = append(exportEnv, "NEXTGO_ENV_DIR=")
	config, err := readExportConfigEnv(ctx, build.Binary, build.ID, *domain, exportEnv)
	if err != nil {
		return err
	}
	if *selected == "" {
		candidates, err := readStaticPathsEnv(ctx, build.Binary, exportEnv, config.Domain)
		if err != nil {
			return err
		}
		paths, err = validateScopedExportPaths(p, candidates, config)
		if err != nil {
			return err
		}
	} else {
		paths, err = expandExportPaths(paths, config.Scopes)
		if err != nil {
			return err
		}
		paths, err = validateScopedExportPaths(p, paths, config)
		if err != nil {
			return err
		}
	}
	base, stop, err := startExportServerEnv(ctx, build.Binary, exportEnv)
	if err != nil {
		return err
	}
	defer stop()
	if err := exportWithScopes(ctx, base, filepath.Join(build.Dir, "server", "assets"), *output, build.ID, paths, config.Paths, config.Scopes, config.Domain); err != nil {
		return err
	}
	dir, _ := filepath.Abs(*output)
	fmt.Printf("Exported %d pages to %s\nServe this directory with a static HTTP server.\n", len(paths), dir)
	return nil
}

func exportPaths(p *project.Project, selected string) ([]string, error) {
	var candidates []string
	if selected != "" {
		candidates = strings.Split(selected, ",")
	} else {
		for _, route := range p.Routes {
			if !route.API && !strings.ContainsAny(route.Pattern, ":*") {
				candidates = append(candidates, route.Pattern)
			}
		}
	}
	return validateExportPaths(p, candidates)
}

func validateExportPaths(p *project.Project, candidates []string) ([]string, error) {
	seen := map[string]bool{}
	files := map[string]string{}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		canonical, decoded, err := exportPath(strings.TrimSpace(candidate))
		if err != nil {
			return nil, err
		}
		if seen[canonical] {
			continue
		}
		var matched *project.Route
		for i := range p.Routes {
			if _, ok := routing.Match(p.Routes[i].Pattern, canonical); ok && (matched == nil || routing.Less(p.Routes[i].Pattern, matched.Pattern)) {
				matched = &p.Routes[i]
			}
		}
		if matched == nil || matched.API {
			return nil, fmt.Errorf("export path %q must resolve to a page route", candidate)
		}
		jsonFile := exportDataPath(decoded)
		if previous, exists := files[jsonFile]; exists {
			return nil, fmt.Errorf("export paths %q and %q produce the same navigation file", previous, canonical)
		}
		files[jsonFile] = canonical
		seen[canonical] = true
		paths = append(paths, canonical)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no static pages to export; provide concrete dynamic paths with -paths")
	}
	sort.Strings(paths)
	return paths, nil
}

// Capture output without retaining an unbounded application's diagnostic stream.
type cappedOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
func (b *cappedOutput) String() string { return b.buffer.String() }
func (b *cappedOutput) Bytes() []byte  { return b.buffer.Bytes() }
func readStaticPaths(ctx context.Context, binary string, domains ...string) ([]string, error) {
	return readStaticPathsEnv(ctx, binary, nil, domains...)
}
func readStaticPathsEnv(ctx context.Context, binary string, env []string, domains ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	output := &cappedOutput{limit: 32 << 20}
	diagnostic := &cappedOutput{limit: 64 << 10}
	args := []string{"-static-paths"}
	if len(domains) > 0 {
		args = append(args, "-export-domain", domains[0])
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = env
	command.Stdout = output
	command.Stderr = diagnostic
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("enumerate static paths: %w", ctx.Err())
		}
		return nil, fmt.Errorf("enumerate static paths: %w: %s", err, diagnostic.String())
	}
	if output.overflow {
		return nil, fmt.Errorf("static path manifest exceeds 32 MiB")
	}
	var paths []string
	if err := json.Unmarshal(output.Bytes(), &paths); err != nil {
		return nil, fmt.Errorf("static paths must be a JSON list; write application build logs to stderr: %w", err)
	}
	if len(paths) > 10000 {
		return nil, fmt.Errorf("static path manifest exceeds 10000 paths")
	}
	return paths, nil
}

// exportPath separates URL spelling from filesystem spelling, rejecting paths
// whose decoded segments could escape the output root or resolve differently.
func exportPath(value string) (canonical, decoded string, err error) {
	u, parseErr := url.Parse(value)
	if parseErr != nil || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") {
		return "", "", fmt.Errorf("unsafe export path %q: use a local absolute path without query or fragment", value)
	}
	if value == "/" {
		return "/", "/", nil
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	for i, part := range parts {
		part, err = url.PathUnescape(part)
		if err != nil || !utf8.ValidString(part) || part == "" || part == "." || part == ".." || strings.ContainsAny(part, "/\\") {
			return "", "", fmt.Errorf("unsafe export path %q", value)
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return "", "", fmt.Errorf("unsafe export path %q", value)
			}
		}
		parts[i] = part
	}
	decoded = "/" + strings.Join(parts, "/")
	u = &url.URL{Path: decoded}
	return u.EscapedPath(), decoded, nil
}

func exportDataPath(decoded string) string {
	if decoded == "/" {
		return filepath.Join("_nextgo", "data", "index.json")
	}
	return filepath.Join("_nextgo", "data", filepath.FromSlash(strings.TrimPrefix(decoded, "/"))+".json")
}

func exportApplication(ctx context.Context, base, assets, output, buildID string, paths []string, options ...nextgo.PathConfig) error {
	deployment := nextgo.PathConfig{}
	if len(options) > 0 {
		deployment = options[0]
	}
	return exportWithScopes(ctx, base, assets, output, buildID, paths, deployment, nil)
}

func exportWithScopes(ctx context.Context, base, assets, output, buildID string, paths []string, deployment nextgo.PathConfig, scopes []nextgo.StaticExportScope, domains ...string) error {
	domain := ""
	if len(domains) > 1 {
		return fmt.Errorf("export requires at most one domain")
	}
	if len(domains) == 1 {
		domain = domains[0]
	}
	if err := validateExportDomain(domain); err != nil {
		return err
	}
	if err := deployment.Validate(); err != nil {
		return err
	}
	if scopes != nil {
		if err := validateExportScopes(deployment, scopes); err != nil {
			return err
		}
	}
	mount := filepath.FromSlash(strings.TrimPrefix(deployment.BasePath, "/"))
	assetMount := filepath.FromSlash(strings.TrimPrefix(deployment.AssetPath(), "/"))
	destination, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".nextgo-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, asset := range []struct{ source, target string }{{"_nextgo", "_nextgo"}, {"public", "assets"}} {
		if err := copyExportTree(filepath.Join(assets, asset.source), filepath.Join(stage, assetMount, asset.target)); err != nil {
			return err
		}
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	configurationID := ""
	for _, pagePath := range paths {
		canonical, decoded, err := exportPath(pagePath)
		if err != nil {
			return err
		}
		dataPath := exportDataPath(decoded)
		expectedConfiguration := configurationID
		if scopes != nil {
			scope, _, ok := exportScope(scopes, canonical)
			if !ok {
				return fmt.Errorf("cannot export %s: undeclared ownership scope", canonical)
			}
			expectedConfiguration = scope.Configuration
			dataPath = scopedExportDataPath(scope.Prefix, decoded)
		}
		publicPath, err := deployment.URL(canonical)
		if err != nil {
			return err
		}
		html, err := exportResponse(ctx, client, base+publicPath, false, domain)
		if err != nil {
			return err
		}
		for _, unsupported := range []string{"<nextgo-slot ", "<template data-nextgo-chunk=", "/_nextgo/actions/", "/_nextgo/image?"} {
			if bytes.Contains(html, []byte(unsupported)) {
				return fmt.Errorf("cannot export %s: parallel slots, streaming, server actions, and optimized image endpoints require a server", canonical)
			}
		}
		body := bytes.Index(html, []byte("<body "))
		if body < 0 || !bytes.Contains(html, []byte(`data-nextgo-build="`+buildID+`"`)) {
			return fmt.Errorf("cannot export %s: response is not the expected Next.go document", canonical)
		}
		html = bytes.Replace(html, []byte("<body "), []byte(`<body data-nextgo-export="true" `), 1)
		data, err := exportResponse(ctx, client, base+publicPath, true, domain)
		if err != nil {
			return err
		}
		var envelope nextgo.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil || envelope.Version != nextgo.NavigationProtocolVersion || envelope.Status != http.StatusOK || envelope.Build != buildID || envelope.URL != publicPath {
			return fmt.Errorf("cannot export %s: invalid navigation response", canonical)
		}
		if !exportDocumentMatches(html, envelope.Document) {
			return fmt.Errorf("cannot export %s: document attributes changed between HTML and navigation data", canonical)
		}
		if err := envelope.Document.Validate(); err != nil {
			return fmt.Errorf("cannot export %s: invalid document attributes: %w", canonical, err)
		}
		openingEnd := bytes.IndexByte(html[body:], '>')
		if !navigation.ValidConfigurationID(envelope.Configuration) || openingEnd < 0 || !bytes.Contains(html[body:body+openingEnd], []byte(`data-nextgo-configuration="`+envelope.Configuration+`"`)) || expectedConfiguration != "" && expectedConfiguration != envelope.Configuration {
			return fmt.Errorf("cannot export %s: deployment configuration changed during export", canonical)
		}
		configurationID = envelope.Configuration
		for name, content := range map[string][]byte{
			filepath.Join(filepath.FromSlash(strings.TrimPrefix(decoded, "/")), "index.html"): html,
			dataPath: data,
		} {
			path := filepath.Join(stage, mount, name)
			if _, err := os.Lstat(path); err == nil {
				return fmt.Errorf("export page conflicts with an asset or page at %s", name)
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(path, content, 0644); err != nil {
				return err
			}
		}
	}
	publication := exportManifest{Format: "nextgo-static-v1", Protocol: nextgo.NavigationProtocolVersion, Configuration: configurationID, Build: buildID, Paths: paths, BasePath: deployment.BasePath, AssetPrefix: deployment.AssetPrefix, TrailingSlash: deployment.TrailingSlash}
	for _, scope := range scopes {
		if scope.Locale != "" {
			publication.Format, publication.Configuration, publication.Scopes = "nextgo-static-v2", "", scopes
			break
		}
	}
	if domain != "" && publication.Format != "nextgo-static-v2" {
		return fmt.Errorf("domain export requires declared locale ownership scopes")
	}
	publication.Domain = domain
	manifest, err := json.Marshal(publication)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, ".nextgo-export.json"), manifest, 0644); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishExport(stage, destination)
}

func exportResponse(ctx context.Context, client *http.Client, target string, navigation bool, domain string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Override only Host. The network target stays the private export server;
	// configured public domain names are never contacted during a build.
	if domain != "" {
		request.Host = domain
	}
	if navigation {
		request.Header.Set("X-Nextgo-Navigate", "1")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.Header.Get("X-Nextgo-Nonce") != "" {
		return nil, fmt.Errorf("cannot export %s: per-request CSP nonces require dynamic rendering", target)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot export %s: HTTP %d", target, response.StatusCode)
	}
	expectedType := "text/html"
	if navigation {
		expectedType = "application/json"
	}
	if navigation && response.Header.Get("X-Nextgo-Protocol") != fmt.Sprint(nextgo.NavigationProtocolVersion) {
		return nil, fmt.Errorf("cannot export %s: missing navigation protocol marker", target)
	}
	if !strings.Contains(response.Header.Get("Content-Type"), expectedType) {
		return nil, fmt.Errorf("cannot export %s: expected %s", target, expectedType)
	}
	const maxBody = 16 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err == nil && len(data) > maxBody {
		err = fmt.Errorf("cannot export %s: page exceeds 16 MiB", target)
	}
	return data, err
}

func copyExportTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("export assets cannot contain symlinks: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0644)
	})
}

// Publication replaces a completed directory, rolling back if the final rename
// fails. This prepares offline artifacts; it is not a zero-downtime deployment.
func publishExport(stage, destination string) error {
	if err := validateExportDestination(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); os.IsNotExist(err) {
		return os.Rename(stage, destination)
	} else if err != nil {
		return err
	}
	backup, err := os.MkdirTemp(filepath.Dir(destination), ".nextgo-export-previous-*")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(destination, backup); err != nil {
		return err
	}
	if err := os.Rename(stage, destination); err != nil {
		if rollback := os.Rename(backup, destination); rollback != nil {
			return fmt.Errorf("publish failed: %v; restoring previous export failed: %v (saved at %s)", err, rollback, backup)
		}
		return err
	}
	return os.RemoveAll(backup)
}

type exportManifest struct {
	Protocol      int                        `json:"protocol"`
	Configuration string                     `json:"configuration"`
	TrailingSlash bool                       `json:"trailingSlash,omitempty"`
	BasePath      string                     `json:"basePath,omitempty"`
	AssetPrefix   string                     `json:"assetPrefix,omitempty"`
	Format        string                     `json:"format"`
	Build         string                     `json:"build"`
	Paths         []string                   `json:"paths"`
	Scopes        []nextgo.StaticExportScope `json:"scopes,omitempty"`
	Domain        string                     `json:"domain,omitempty"`
}

func validateExportDestination(destination string) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("export destination must be a directory: %s", destination)
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) == 0 {
		return err
	}
	data, err := os.ReadFile(filepath.Join(destination, ".nextgo-export.json"))
	var manifest exportManifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Format != "nextgo-static-v1" && manifest.Format != "nextgo-static-v2" || manifest.Build == "" || len(manifest.Paths) == 0 {
		return fmt.Errorf("refusing to replace nonempty directory %s: it is not a previous Next.go export", destination)
	}
	if manifest.Format == "nextgo-static-v2" {
		if err := validateExportDomain(manifest.Domain); err != nil {
			return fmt.Errorf("refusing to replace invalid domain export %s: %w", destination, err)
		}
		if err := validateExportScopes(nextgo.PathConfig{BasePath: manifest.BasePath, AssetPrefix: manifest.AssetPrefix, TrailingSlash: manifest.TrailingSlash}, manifest.Scopes); err != nil {
			return fmt.Errorf("refusing to replace invalid scoped export %s: %w", destination, err)
		}
	}
	return nil
}

func startExportServer(ctx context.Context, binary string) (string, func(), error) {
	return startExportServerEnv(ctx, binary, nil)
}
func startExportServerEnv(ctx context.Context, binary string, env []string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	command := exec.CommandContext(ctx, binary, "-addr", addr)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	// Static export always exercises the native HTTP path, including in CI that
	// happens to provide Lambda-related environment variables.
	if env == nil {
		env = os.Environ()
	}
	command.Env = append(append([]string(nil), env...), "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_ENV_DIR=")
	if err := command.Start(); err != nil {
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stop := func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			stop()
			return "", nil, ctx.Err()
		case err := <-done:
			return "", nil, fmt.Errorf("export server exited during startup: %v", err)
		case <-timeout.C:
			stop()
			return "", nil, fmt.Errorf("export server startup timed out")
		case <-ticker.C:
			connection, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				_ = connection.Close()
				return "http://" + addr, stop, nil
			}
		}
	}
}

// Framework templates own the opening html and viewport tags. Compare only the
// head/outer attributes, never similarly named component nodes in the body.
func exportDocumentMatches(markup []byte, document nextgo.Document) bool {
	if document.Validate() != nil {
		return false
	}
	htmlStart := bytes.Index(markup, []byte("<html "))
	headEnd := bytes.Index(markup, []byte("</head>"))
	if htmlStart < 0 || headEnd < htmlStart {
		return false
	}
	openingEnd := bytes.IndexByte(markup[htmlStart:], '>')
	if openingEnd < 0 {
		return false
	}
	opening := markup[htmlStart : htmlStart+openingEnd]
	return bytes.Contains(opening, []byte(` lang="`+document.Language+`"`)) && bytes.Contains(opening, []byte(` dir="`+document.Direction+`"`)) && bytes.Contains(markup[htmlStart:headEnd], []byte(`<meta name="viewport" content="`+htmlstd.EscapeString(document.Viewport)+`">`))
}
