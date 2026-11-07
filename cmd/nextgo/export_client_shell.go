package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
	"github.com/Suhaibinator/Next.go/ui"
)

func validateStaticShellProject(p *project.Project, shell *nextgo.StaticShellDeclaration) error {
	if shell == nil {
		return nil
	}
	matches := 0
	for _, component := range p.Components {
		if component.Name == shell.Component {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("static shell component must name exactly one generated browser component")
	}
	return nil
}

func staticShellDeferredVary(values []string) bool {
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(name), navigation.MetadataHeader) {
				return true
			}
		}
	}
	return false
}

func stageStaticShell(stage string, deployment nextgo.PathConfig, build string, shell nextgo.StaticShellDeclaration, document, data []byte, envelope nextgo.Envelope) (navigation.StaticShellDescriptor, error) {
	var descriptor navigation.StaticShellDescriptor
	if len(data) == 0 || len(data) > navigation.MaxStaticShellEnvelopeBytes || !utf8.Valid(data) || wirejson.Unique(data) != nil || wirejson.Strict(data, &envelope) != nil || envelope.Shell != "" || envelope.ShellRef {
		return descriptor, fmt.Errorf("invalid bounded generic seed envelope")
	}
	if err := validateStaticShellSeed(document, envelope, shell); err != nil {
		return descriptor, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	seedURL, err := deployment.URL(shell.SeedPath)
	if err != nil {
		return descriptor, err
	}
	envelopeURL, err := navigation.StaticShellEnvelopeURL(deployment, build, digest)
	if err != nil {
		return descriptor, err
	}
	descriptor = navigation.StaticShellDescriptor{Version: navigation.StaticShellVersion, Build: build, Configuration: envelope.Configuration, Pattern: shell.Pattern, Component: shell.Component, SeedURL: seedURL, EnvelopeURL: envelopeURL, SHA256: digest, Bytes: len(data)}
	if _, err := navigation.EncodeStaticShellDescriptor(descriptor, deployment, build, envelope.Configuration); err != nil {
		return descriptor, err
	}
	if envelope.URL != seedURL || envelope.Build != build || envelope.Status != http.StatusOK || envelope.Version != nextgo.NavigationProtocolVersion || envelope.Document.Validate() != nil {
		return descriptor, fmt.Errorf("seed envelope identity changed")
	}
	if err := writeStaticShellFile(stage, envelopeURL, data); err != nil {
		return descriptor, err
	}
	return descriptor, nil
}

func writeStaticShellFile(stage, publicPath string, data []byte) error {
	canonical, decoded, err := exportPath(publicPath)
	if err != nil || canonical != publicPath {
		return fmt.Errorf("invalid static shell output path")
	}
	target := filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(decoded, "/")))
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("static shell output conflicts with an existing artifact")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0644)
}

// The same validated descriptor goes into every exported document. Injection is
// completed in staging, after the immutable seed bytes have been finalized.
func injectStaticShellDocuments(stage string, pagePaths []string, deployment nextgo.PathConfig, descriptor navigation.StaticShellDescriptor) error {
	encoded, err := navigation.EncodeStaticShellDescriptor(descriptor, deployment, descriptor.Build, descriptor.Configuration)
	if err != nil {
		return err
	}
	for _, pagePath := range pagePaths {
		_, decoded, err := exportPath(pagePath)
		if err != nil {
			return err
		}
		file := filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(deployment.BasePath, "/")), filepath.FromSlash(strings.TrimPrefix(decoded, "/")), "index.html")
		markup, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		opening, err := staticShellDocumentBody(markup)
		if err != nil || opening.attrs["data-nextgo-export"] != "true" || opening.attrs["data-nextgo-build"] != descriptor.Build || opening.attrs["data-nextgo-configuration"] != descriptor.Configuration {
			return fmt.Errorf("static shell document publication identity changed")
		}
		if _, present := opening.attrs["data-nextgo-static-shell"]; present {
			return fmt.Errorf("static shell descriptor already exists in source HTML")
		}
		attribute := []byte(` data-nextgo-static-shell="` + html.EscapeString(string(encoded)) + `"`)
		output := make([]byte, 0, len(markup)+len(attribute))
		output = append(output, markup[:opening.end-1]...)
		output = append(output, attribute...)
		output = append(output, markup[opening.end-1:]...)
		if len(output) > 16<<20 {
			return fmt.Errorf("static shell HTML with descriptor exceeds 16 MiB")
		}
		if err := os.WriteFile(file, output, 0644); err != nil {
			return err
		}
	}
	return nil
}

func validateStaticShellSeed(document []byte, envelope nextgo.Envelope, shell nextgo.StaticShellDeclaration) error {
	if err := validateStaticShellFragment([]byte(envelope.HTML), shell.Component); err != nil {
		return err
	}
	for name := range envelope.Meta {
		if strings.EqualFold(name, "og:url") {
			return fmt.Errorf("generic shell cannot publish path-specific og:url")
		}
	}
	for _, element := range envelope.Head {
		tag := strings.ToLower(element.Tag)
		if tag != "title" && tag != "meta" && tag != "link" {
			return fmt.Errorf("unsupported generic shell head element")
		}
		if err := staticShellGenericHead(tag, element.Attrs); err != nil {
			return err
		}
	}
	stack := []string{}
	appStart, appEnd, appDepth, apps, bodies, islands := -1, -1, -1, 0, 0, 0
	err := scanStaticShellHTML(document, func(token staticShellHTMLToken) error {
		if token.tag == "" {
			return nil
		}
		if token.close {
			if len(stack) == 0 || stack[len(stack)-1] != token.tag {
				return fmt.Errorf("unbalanced seed document")
			}
			if appDepth == len(stack) {
				appEnd, appDepth = token.start, -1
			}
			stack = stack[:len(stack)-1]
			return nil
		}
		if strings.HasPrefix(token.tag, "nextgo-") {
			return fmt.Errorf("unsupported framework marker in shell document")
		}
		if _, present := token.attrs["data-nextgo-island"]; present {
			islands++
		}
		if _, present := token.attrs["nonce"]; present {
			return fmt.Errorf("generic shell cannot publish nonce-dependent output")
		}
		if token.tag == "head" || containsStaticShellTag(stack, "head") {
			if err := staticShellGenericHead(token.tag, token.attrs); err != nil {
				return err
			}
		}
		if token.tag == "body" {
			bodies++
			for _, name := range []string{"data-nextgo-preserve-layouts", "data-nextgo-offline", "data-nextgo-offline-saved", "data-nextgo-draft", "data-nextgo-static-shell"} {
				if _, present := token.attrs[name]; present {
					return fmt.Errorf("unsupported generic shell document policy")
				}
			}
			var manifest navigation.Manifest
			if json.Unmarshal([]byte(token.attrs["data-nextgo-navigation"]), &manifest) != nil || manifest.Draft || manifest.Locale != "" || manifest.Rules != "" || manifest.Parallel != "" {
				return fmt.Errorf("unsupported generic shell route manifest")
			}
			first, ok := navigation.FirstMatchingRoute(manifest, shell.SeedPath)
			if !ok || first.Endpoint || first.Pattern != shell.Pattern {
				return fmt.Errorf("seed does not select its declared page")
			}
		}
		if token.attrs["id"] == "nextgo-app" {
			apps++
			if token.tag != "div" || token.void {
				return fmt.Errorf("invalid seed application container")
			}
			appStart, appDepth = token.end, len(stack)+1
		}
		if !token.void {
			stack = append(stack, token.tag)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(stack) != 0 || apps != 1 || bodies != 1 || islands != 1 || appStart < 0 || appEnd < appStart || string(document[appStart:appEnd]) != envelope.HTML {
		return fmt.Errorf("seed HTML differs from its generic navigation body")
	}
	return nil
}

func staticShellGenericHead(tag string, attrs map[string]string) error {
	if tag == "script" && strings.EqualFold(attrs["type"], "application/ld+json") {
		return fmt.Errorf("generic shell cannot publish JSONLD")
	}
	if tag == "link" {
		for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
			if rel == "canonical" || rel == "alternate" {
				return fmt.Errorf("generic shell cannot publish canonical or alternate links")
			}
		}
		if _, present := attrs["hreflang"]; present {
			return fmt.Errorf("generic shell cannot publish hreflang")
		}
	}
	if tag == "meta" && (strings.EqualFold(attrs["property"], "og:url") || strings.EqualFold(attrs["name"], "og:url")) {
		return fmt.Errorf("generic shell cannot publish path-specific og:url")
	}
	for name := range attrs {
		if strings.HasPrefix(strings.ToLower(name), "data-nextgo-metadata-") {
			return fmt.Errorf("generic shell cannot publish deferred metadata")
		}
	}
	return nil
}

func validateStaticShellFragment(fragment []byte, component string) error {
	stack := []string{}
	islands, clients, boundaryDepth, fallbackStart, fallbackElements := 0, 0, -1, -1, 0
	fallbackUseful := false
	err := scanStaticShellHTML(fragment, func(token staticShellHTMLToken) error {
		if token.tag == "" {
			if token.comment {
				return fmt.Errorf("generic shell contains unsupported comments or directives")
			}
			if boundaryDepth >= 0 && strings.TrimSpace(html.UnescapeString(string(fragment[token.start:token.end]))) != "" {
				fallbackUseful = true
			}
			return nil
		}
		if token.close {
			if len(stack) == 0 || stack[len(stack)-1] != token.tag {
				return fmt.Errorf("unbalanced generic shell body")
			}
			if boundaryDepth == len(stack) {
				if token.start-fallbackStart > 64<<10 || !fallbackUseful {
					return fmt.Errorf("client shell requires a useful fallback within 64 KiB")
				}
				boundaryDepth = -1
			}
			stack = stack[:len(stack)-1]
			return nil
		}
		if strings.HasPrefix(token.tag, "nextgo-") {
			return fmt.Errorf("generic shell contains route/layout/slot markers")
		}
		if _, present := token.attrs["data-nextgo-island"]; present {
			islands++
			if islands != 1 || token.tag != "div" || token.void || token.attrs["data-nextgo-island"] != component || token.attrs["data-nextgo-client"] != "1" || !json.Valid([]byte(token.attrs["data-nextgo-props"])) {
				return fmt.Errorf("generic shell requires exactly its declared ClientIsland")
			}
			clients++
			boundaryDepth, fallbackStart = len(stack)+1, token.end
		}
		for name := range token.attrs {
			if !strings.HasPrefix(name, "data-nextgo-") {
				continue
			}
			if boundaryDepth >= 0 && boundaryDepth <= len(stack) {
				return fmt.Errorf("client fallback contains framework markers")
			}
			switch name {
			case "data-nextgo-island", "data-nextgo-props", "data-nextgo-client":
				if token.attrs["data-nextgo-island"] != component {
					return fmt.Errorf("orphan generic shell client marker")
				}
			default:
				return fmt.Errorf("generic shell contains selection/stream/region markers")
			}
		}
		// Reuse the existing element, attribute and URL validation on actual
		// rendered bytes, including trusted custom Node fallback output.
		switch token.tag {
		case "template", "link", "meta", "svg", "math", "plaintext", "xmp", "noembed", "noframes", "noscript":
			return fmt.Errorf("unsupported generic shell element %s", token.tag)
		}
		if err := ui.RenderTo(context.Background(), io.Discard, ui.El(token.tag, ui.Attrs(token.attrs))); err != nil {
			return fmt.Errorf("invalid generic shell element: %w", err)
		}
		if boundaryDepth >= 0 && boundaryDepth <= len(stack) {
			fallbackElements++
			if fallbackElements > 2000 {
				return fmt.Errorf("client fallback exceeds 2000 elements")
			}
			if token.tag == "img" && strings.TrimSpace(token.attrs["alt"]) != "" {
				fallbackUseful = true
			}
		}
		if !token.void {
			stack = append(stack, token.tag)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(stack) != 0 || islands != 1 || clients != 1 || boundaryDepth >= 0 {
		return fmt.Errorf("generic shell requires one complete ClientIsland")
	}
	return nil
}

// Validate a completed v3 destination before it may be replaced. The descriptor
// and immutable bytes are checked together, using bounded, regular-file reads.
func validateStaticShellDestination(root string, manifestData []byte, manifest exportManifest) error {
	if len(manifestData) > 32<<20 || !utf8.Valid(manifestData) || wirejson.Unique(manifestData) != nil || wirejson.Strict(manifestData, &manifest) != nil || manifest.StaticShell == nil || manifest.Protocol != nextgo.NavigationProtocolVersion || manifest.Domain != "" || len(manifest.Scopes) != 0 || len(manifest.Paths) > 10000 {
		return fmt.Errorf("invalid static shell publication manifest")
	}
	deployment := nextgo.PathConfig{BasePath: manifest.BasePath, AssetPrefix: manifest.AssetPrefix, TrailingSlash: manifest.TrailingSlash}
	descriptor := *manifest.StaticShell
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	if _, err := navigation.DecodeStaticShellDescriptor(encoded, deployment, manifest.Build, manifest.Configuration); err != nil {
		return err
	}
	if err := validateMetadataInventory(deployment, manifest.Metadata); err != nil {
		return err
	}
	data, err := readStaticShellArtifact(root, descriptor.EnvelopeURL, navigation.MaxStaticShellEnvelopeBytes)
	if err != nil || descriptor.CheckEnvelope(data) != nil {
		return fmt.Errorf("invalid immutable static shell envelope")
	}
	var envelope nextgo.Envelope
	if wirejson.Unique(data) != nil || wirejson.Strict(data, &envelope) != nil || envelope.Build != descriptor.Build || envelope.Configuration != descriptor.Configuration || envelope.URL != descriptor.SeedURL || envelope.Version != nextgo.NavigationProtocolVersion || envelope.Status != http.StatusOK || envelope.Document.Validate() != nil || envelope.Shell != "" || envelope.ShellRef {
		return fmt.Errorf("invalid published static shell seed identity")
	}
	seed, err := navigation.ValidateStaticShellDeclaration(descriptor.Pattern, descriptor.Component)
	if err != nil {
		return err
	}
	found, seen, totalPathBytes := false, map[string]bool{}, 0
	for _, pagePath := range manifest.Paths {
		totalPathBytes += len(pagePath)
		if len(pagePath) > 8192 || totalPathBytes > 16<<20 {
			return fmt.Errorf("published static shell page paths exceed bounds")
		}
		canonical, _, err := exportPath(pagePath)
		if err != nil || canonical != pagePath || seen[pagePath] {
			return fmt.Errorf("invalid published static shell page path")
		}
		seen[pagePath] = true
		markup, err := readStaticShellArtifact(root, deployment.BasePath+strings.TrimSuffix(pagePath, "/")+"/index.html", 16<<20)
		if err != nil {
			return err
		}
		body, err := staticShellDocumentBody(markup)
		if err != nil || body.attrs["data-nextgo-export"] != "true" || body.attrs["data-nextgo-build"] != descriptor.Build || body.attrs["data-nextgo-configuration"] != descriptor.Configuration || body.attrs["data-nextgo-static-shell"] != string(encoded) {
			return fmt.Errorf("published static shell HTML descriptor mismatch")
		}
		if pagePath == seed {
			found = true
			// Remove only the publication carrier for the source seed checker.
			without := bytes.Replace(markup, []byte(` data-nextgo-static-shell="`+html.EscapeString(string(encoded))+`"`), nil, 1)
			if !exportDocumentMatches(without, envelope.Document) || validateStaticShellSeed(without, envelope, nextgo.StaticShellDeclaration{Version: 1, Pattern: descriptor.Pattern, Component: descriptor.Component, SeedPath: seed}) != nil {
				return fmt.Errorf("published static shell HTML seed mismatch")
			}
		}
	}
	if !found {
		return fmt.Errorf("published static shell is missing its seed page")
	}
	return nil
}

func readStaticShellArtifact(root, publicPath string, limit int) ([]byte, error) {
	canonical, decoded, err := exportPath(publicPath)
	if err != nil || canonical != publicPath {
		return nil, fmt.Errorf("invalid static shell artifact path")
	}
	current := root
	parts := strings.Split(strings.TrimPrefix(decoded, "/"), "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() > int64(limit)) {
			return nil, fmt.Errorf("invalid static shell artifact file")
		}
	}
	file, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, fmt.Errorf("static shell artifact exceeds bounds")
	}
	return data, nil
}

type staticShellHTMLToken struct {
	tag        string
	attrs      map[string]string
	start, end int
	close      bool
	void       bool
	comment    bool
}

func staticShellDocumentBody(markup []byte) (staticShellHTMLToken, error) {
	var body staticShellHTMLToken
	count := 0
	err := scanStaticShellHTML(markup, func(token staticShellHTMLToken) error {
		if token.tag == "body" && !token.close {
			body = token
			count++
		}
		return nil
	})
	if err != nil || count != 1 {
		return body, fmt.Errorf("expected one static export body")
	}
	return body, nil
}

func containsStaticShellTag(stack []string, tag string) bool {
	for _, entry := range stack {
		if entry == tag {
			return true
		}
	}
	return false
}

// Scan only the framework's bounded HTML subset. This is a conservative export
// validator, not a sanitizer or general browser parser. Foreign/raw ambiguous
// fallback content is refused, and script/style raw text is never read as tags.
func scanStaticShellHTML(data []byte, visit func(staticShellHTMLToken) error) error {
	if len(data) > 16<<20 || !utf8.Valid(data) {
		return fmt.Errorf("invalid static shell HTML bounds")
	}
	lower := bytes.ToLower(data)
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
	name := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == ':' || c == '_' || c == '.'
	}
	voids := map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}
	count, raw := 0, ""
	for offset := 0; offset < len(data); {
		count++
		if count > 262144 {
			return fmt.Errorf("static shell HTML token limit exceeded")
		}
		start := offset
		if raw != "" {
			needle := []byte("</" + raw)
			relative := bytes.Index(lower[offset:], needle)
			for relative >= 0 {
				end := offset + relative + len(needle)
				if end < len(data) && (space(data[end]) || data[end] == '>') {
					break
				}
				next := bytes.Index(lower[end:], needle)
				if next < 0 {
					relative = -1
				} else {
					relative = end + next - offset
				}
			}
			if relative < 0 {
				return fmt.Errorf("unclosed static shell raw-text element")
			}
			offset += relative
			raw = ""
			if offset > start {
				if err := visit(staticShellHTMLToken{start: start, end: offset}); err != nil {
					return err
				}
				continue
			}
		}
		if data[offset] != '<' {
			next := bytes.IndexByte(data[offset:], '<')
			if next < 0 {
				next = len(data) - offset
			}
			offset += next
			if err := visit(staticShellHTMLToken{start: start, end: offset}); err != nil {
				return err
			}
			continue
		}
		if bytes.HasPrefix(data[offset:], []byte("<!--")) {
			end := bytes.Index(data[offset+4:], []byte("-->"))
			if end < 0 {
				return fmt.Errorf("unclosed static shell comment")
			}
			offset += end + 7
			if err := visit(staticShellHTMLToken{start: start, end: offset, comment: true}); err != nil {
				return err
			}
			continue
		}
		if bytes.HasPrefix(lower[offset:], []byte("<!doctype html>")) {
			offset += len("<!doctype html>")
			if err := visit(staticShellHTMLToken{start: start, end: offset, comment: true}); err != nil {
				return err
			}
			continue
		}
		offset++
		token := staticShellHTMLToken{start: start, attrs: map[string]string{}}
		if offset < len(data) && data[offset] == '/' {
			token.close = true
			offset++
		}
		begin := offset
		for offset < len(data) && name(data[offset]) {
			offset++
		}
		if begin == offset {
			return fmt.Errorf("invalid static shell HTML tag")
		}
		token.tag = strings.ToLower(string(data[begin:offset]))
		for {
			before := offset
			for offset < len(data) && space(data[offset]) {
				offset++
			}
			if offset >= len(data) {
				return fmt.Errorf("unclosed static shell HTML tag")
			}
			if data[offset] == '>' {
				offset++
				break
			}
			if data[offset] == '/' && offset+1 < len(data) && data[offset+1] == '>' {
				token.void = true
				offset += 2
				break
			}
			if token.close || before == offset {
				return fmt.Errorf("invalid static shell HTML attributes")
			}
			begin = offset
			for offset < len(data) && name(data[offset]) {
				offset++
			}
			if begin == offset {
				return fmt.Errorf("invalid static shell HTML attribute")
			}
			key := strings.ToLower(string(data[begin:offset]))
			if _, duplicate := token.attrs[key]; duplicate {
				return fmt.Errorf("duplicate static shell HTML attribute")
			}
			nameEnd := offset
			for offset < len(data) && space(data[offset]) {
				offset++
			}
			value := ""
			if offset < len(data) && data[offset] == '=' {
				offset++
				for offset < len(data) && space(data[offset]) {
					offset++
				}
				if offset >= len(data) {
					return fmt.Errorf("missing static shell attribute value")
				}
				quote := data[offset]
				if quote == '\'' || quote == '"' {
					offset++
					begin = offset
					for offset < len(data) && data[offset] != quote {
						offset++
					}
					if offset >= len(data) {
						return fmt.Errorf("unclosed static shell attribute value")
					}
					value = html.UnescapeString(string(data[begin:offset]))
					offset++
				} else {
					begin = offset
					for offset < len(data) && !space(data[offset]) && data[offset] != '>' {
						if strings.ContainsRune("<\"'=`", rune(data[offset])) {
							return fmt.Errorf("invalid unquoted static shell attribute")
						}
						offset++
					}
					if begin == offset {
						return fmt.Errorf("empty unquoted static shell attribute")
					}
					value = html.UnescapeString(string(data[begin:offset]))
				}
			} else {
				// Preserve the separator before the next boolean attribute.
				offset = nameEnd
			}
			token.attrs[key] = value
		}
		token.end = offset
		token.void = token.void || voids[token.tag]
		if token.close && token.void || token.void && !voids[token.tag] {
			return fmt.Errorf("ambiguous self-closing static shell element")
		}
		if err := visit(token); err != nil {
			return err
		}
		if !token.close && !token.void && (token.tag == "script" || token.tag == "style" || token.tag == "title" || token.tag == "textarea") {
			raw = token.tag
		}
	}
	if raw != "" {
		return fmt.Errorf("unclosed static shell raw-text element")
	}
	return nil
}
