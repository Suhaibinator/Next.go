package nextgo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func sharedCodecFixture(t *testing.T) cachedPage {
	t.Helper()
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "Article", "name": "</script><img src=x onerror=alert(1)>"})
	if err != nil {
		t.Fatal(err)
	}
	integrity := "sha256-" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	page := Page{
		Title: "Public <title>", Description: "Public <description>", Meta: map[string]string{"author": "Ada <Lovelace>"},
		Metadata: seo.Metadata{Language: "ar", Direction: "rtl", Canonical: "https://example.test/posts/a", Robots: "index,follow",
			Alternates: map[string]string{"ar": "https://example.test/ar/posts/a"},
			Icons:      []seo.Icon{{URL: "/assets/icon.svg", Type: "image/svg+xml"}},
			Viewport:   &seo.Viewport{Width: "device-width", InitialScale: 1, ThemeColors: []seo.ThemeColor{{Color: "#123", Media: "(prefers-color-scheme: dark)"}}}},
		JSONLD:    []seo.StructuredData{structured},
		Resources: []assets.Resource{assets.Stylesheet("/assets/page.css", integrity), assets.Font{Family: "Public", Source: "/assets/public.woff2", Format: "woff2"}.Preload()},
	}
	head, markup, err := pageHeadPaths(page, paths.Config{BasePath: "/app", AssetPrefix: "https://cdn.example.test/static"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := pageDocument(page)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ui.HTML(ui.El("p", nil, ui.Text("Useful public <body>")))
	if err != nil {
		t.Fatal(err)
	}
	return cachedPage{HTML: body, GeneratedAt: time.Date(2026, 10, 24, 10, 30, 0, 123, time.FixedZone("example", -7*60*60)),
		Page: Page{Title: page.Title, Description: page.Description, Meta: page.Meta, document: &document, preparedHead: &renderedHead{elements: head, markup: markup}}}
}

func TestSharedPageSnapshotRoundTripRichHeadAndDocument(t *testing.T) {
	value := sharedCodecFixture(t)
	dto, err := encodeSharedPage(value, "binding-v1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "headHTML") || strings.Contains(string(data), value.Page.preparedHead.markup) {
		t.Fatal("snapshot persists redundant raw head markup")
	}
	var transferred sharedPageSnapshot
	if err := json.Unmarshal(data, &transferred); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSharedPage(transferred, "binding-v1")
	if err != nil {
		t.Fatal(err)
	}
	if decoded.HTML != value.HTML || decoded.Page.Title != value.Page.Title || decoded.Page.Description != value.Page.Description || !decoded.GeneratedAt.Equal(value.GeneratedAt) || decoded.GeneratedAt.Location() != time.UTC {
		t.Fatal("snapshot values/age changed", decoded)
	}
	if *decoded.Page.document != *value.Page.document || decoded.Page.document.Language != "ar" || decoded.Page.document.Direction != "rtl" {
		t.Fatal("document mismatch", decoded.Page.document)
	}
	if decoded.Page.preparedHead.markup != value.Page.preparedHead.markup {
		t.Fatalf("SSR/navigation head diverged\noriginal: %s\ndecoded: %s", value.Page.preparedHead.markup, decoded.Page.preparedHead.markup)
	}
	if decoded.Page.Body != nil || decoded.Page.Resources != nil || decoded.Page.JSONLD != nil || decoded.Page.Stream || decoded.Prerendered {
		t.Fatal("decoded snapshot retains runtime components or false provenance")
	}
	if _, _, err := pageHead(decoded.Page); err != nil {
		t.Fatal(err)
	}
}

func TestSharedPageSnapshotOwnsMapsAndIgnoresStoredMarkup(t *testing.T) {
	value := sharedCodecFixture(t)
	value.Page.preparedHead.markup = `<script src="https://bad.example/execute.js"></script>`
	dto, err := encodeSharedPage(value, "binding")
	if err != nil {
		t.Fatal(err)
	}
	value.Page.Meta["author"] = "changed source"
	value.Page.preparedHead.elements[0].Attrs["name"] = "changed-source"
	if dto.Meta["author"] != "Ada <Lovelace>" || dto.Head[0].Attrs["name"] == "changed-source" {
		t.Fatal("encoded maps alias the source")
	}
	decoded, err := decodeSharedPage(dto, "binding")
	if err != nil {
		t.Fatal(err)
	}
	dto.Meta["author"] = "changed DTO"
	dto.Head[0].Attrs["name"] = "changed-dto"
	if decoded.Page.Meta["author"] != "Ada <Lovelace>" || decoded.Page.preparedHead.elements[0].Attrs["name"] == "changed-dto" || strings.Contains(decoded.Page.preparedHead.markup, "bad.example") {
		t.Fatal("decoded data aliases DTO or trusts raw source markup")
	}
}

func TestSharedPageSnapshotReescapesStoredJSONLD(t *testing.T) {
	dto, err := encodeSharedPage(sharedCodecFixture(t), "binding")
	if err != nil {
		t.Fatal(err)
	}
	dto.Head = []seo.Element{{Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `{"name":"</script><img src=x onerror=alert(1)>"}`}}
	decoded, err := decodeSharedPage(dto, "binding")
	if err != nil {
		t.Fatal(err)
	}
	markup := decoded.Page.preparedHead.markup
	if strings.Count(markup, "</script>") != 1 || strings.Contains(markup, "<img") || strings.Contains(decoded.Page.preparedHead.elements[0].Text, "</script>") {
		t.Fatal("stored JSON-LD escaped its inert node", markup)
	}
	if !strings.Contains(markup, `\u003c/script\u003e`) {
		t.Fatal("JSON-LD was not freshly escaped", markup)
	}
}

func TestSharedPageSnapshotRejectsIdentityMetadataAndBounds(t *testing.T) {
	changes := map[string]func(*sharedPageSnapshot){
		"format":              func(v *sharedPageSnapshot) { v.Format++ },
		"binding":             func(v *sharedPageSnapshot) { v.Binding = "other" },
		"zero-time":           func(v *sharedPageSnapshot) { v.GeneratedAt = time.Time{} },
		"unserializable-time": func(v *sharedPageSnapshot) { v.GeneratedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"body-size":           func(v *sharedPageSnapshot) { v.HTML = strings.Repeat("x", maxSharedPageBody+1) },
		"body-utf8":           func(v *sharedPageSnapshot) { v.HTML = string([]byte{255}) },
		"body-nul":            func(v *sharedPageSnapshot) { v.HTML = "bad\x00body" },
		"title-size":          func(v *sharedPageSnapshot) { v.Title = strings.Repeat("x", maxSharedPageText+1) },
		"meta-case":           func(v *sharedPageSnapshot) { v.Meta = map[string]string{"Author": "one", "author": "two"} },
		"meta-noncanonical":   func(v *sharedPageSnapshot) { v.Meta = map[string]string{"Author": "one"} },
		"meta-viewport":       func(v *sharedPageSnapshot) { v.Meta = map[string]string{"viewport": "other"} },
		"meta-description":    func(v *sharedPageSnapshot) { v.Meta = map[string]string{"description": "duplicate"} },
		"meta-name":           func(v *sharedPageSnapshot) { v.Meta = map[string]string{"": "empty"} },
		"meta-value": func(v *sharedPageSnapshot) {
			v.Meta = map[string]string{"author": strings.Repeat("x", maxSharedPageText+1)}
		},
		"meta-budget": func(v *sharedPageSnapshot) {
			v.Title = strings.Repeat("x", maxSharedPageText)
			v.Description = v.Title
			v.Meta = map[string]string{"a": v.Title, "b": v.Title}
		},
		"head-count": func(v *sharedPageSnapshot) { v.Head = make([]seo.Element, maxSharedPageHeadElements+1) },
		"head-budget": func(v *sharedPageSnapshot) {
			v.Head = make([]seo.Element, 17)
			for i := range v.Head {
				v.Head[i] = seo.Element{Tag: "meta", Attrs: map[string]string{"property": "og:image:alt", "content": strings.Repeat("x", maxSharedPageText)}}
			}
		},
		"jsonld-count": func(v *sharedPageSnapshot) {
			v.Head = make([]seo.Element, 17)
			for i := range v.Head {
				v.Head[i] = seo.Element{Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `{}`}
			}
		},
		"jsonld-budget": func(v *sharedPageSnapshot) {
			v.Head = make([]seo.Element, 3)
			for i := range v.Head {
				v.Head[i] = seo.Element{Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `{"name":"` + strings.Repeat("x", 180000) + `"}`}
			}
		},
		"jsonld-escape-budget": func(v *sharedPageSnapshot) {
			v.Head = []seo.Element{{Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `{"name":"` + strings.Repeat("<", 50000) + `"}`}}
		},
		"language":  func(v *sharedPageSnapshot) { v.Document.Language = "bad\"lang" },
		"direction": func(v *sharedPageSnapshot) { v.Document.Direction = "sideways" },
		"viewport":  func(v *sharedPageSnapshot) { v.Document.Viewport = "bad\nviewport" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			dto, err := encodeSharedPage(sharedCodecFixture(t), "binding")
			if err != nil {
				t.Fatal(err)
			}
			change(&dto)
			if _, err := decodeSharedPage(dto, "binding"); !errors.Is(err, ErrSharedPageSnapshot) {
				t.Fatal(err)
			}
		})
	}
	if _, err := encodeSharedPage(sharedCodecFixture(t), ""); !errors.Is(err, ErrSharedPageSnapshot) {
		t.Fatal(err)
	}
	if _, err := encodeSharedPage(cachedPage{}, "binding"); !errors.Is(err, ErrSharedPageSnapshot) {
		t.Fatal(err)
	}
}

func TestSharedPageSnapshotRejectsDangerousHeadDescriptors(t *testing.T) {
	for name, element := range map[string]seo.Element{
		"executable-script":  {Tag: "script", Attrs: map[string]string{"type": "text/javascript"}, Text: "alert(1)"},
		"script-src":         {Tag: "script", Attrs: map[string]string{"type": "application/ld+json", "src": "https://bad.example/"}, Text: "{}"},
		"script-primitive":   {Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `"string"`},
		"script-invalid":     {Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `{`},
		"base":               {Tag: "base", Attrs: map[string]string{"href": "https://bad.example/"}},
		"title":              {Tag: "title", Text: "unexpected"},
		"event-attribute":    {Tag: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "/assets/a.css", "onload": "alert(1)"}},
		"javascript-url":     {Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "javascript:alert(1)"}},
		"data-url":           {Tag: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "data:text/css,body{}"}},
		"relative-authority": {Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "//bad.example/"}},
		"credentials":        {Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "https://user:secret@example.test/"}},
		"backslash":          {Tag: "link", Attrs: map[string]string{"rel": "icon", "href": "/\\bad.example/icon"}},
		"preload-script":     {Tag: "link", Attrs: map[string]string{"rel": "preload", "href": "/a.js", "as": "script"}},
		"bad-integrity":      {Tag: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "/a.css", "integrity": "sha256-invalid", "crossorigin": "anonymous"}},
		"viewport-owner":     {Tag: "meta", Attrs: map[string]string{"name": "ViewPort", "content": "changed"}},
		"description-owner":  {Tag: "meta", Attrs: map[string]string{"name": "description", "content": "changed"}},
		"http-equiv":         {Tag: "meta", Attrs: map[string]string{"http-equiv": "refresh", "content": "0;url=https://bad.example/"}},
		"meta-event":         {Tag: "meta", Attrs: map[string]string{"name": "robots", "content": "index", "onload": "alert(1)"}},
		"meta-alias":         {Tag: "meta", Attrs: map[string]string{"NAME": "robots", "content": "index"}},
		"meta-both-kinds":    {Tag: "meta", Attrs: map[string]string{"name": "robots", "property": "", "content": "index"}},
		"unexpected-text":    {Tag: "meta", Attrs: map[string]string{"name": "robots", "content": "index"}, Text: "hidden"},
	} {
		t.Run(name, func(t *testing.T) {
			dto, err := encodeSharedPage(sharedCodecFixture(t), "binding")
			if err != nil {
				t.Fatal(err)
			}
			dto.Head = []seo.Element{element}
			if _, err := decodeSharedPage(dto, "binding"); !errors.Is(err, ErrSharedPageSnapshot) {
				t.Fatal(err)
			}
		})
	}
}

func sharedPartialCodecFixture(t *testing.T) cachedPage {
	t.Helper()
	value := sharedCodecFixture(t)
	shell, err := ui.CaptureShell(context.Background(), ui.El("section", nil,
		ui.Text("☃ Static prefix "),
		ui.RequestRegion("account-hole", "account", map[string]any{"label": "public <label>", "n": 1}, ui.El("p", nil, ui.Text("Public account fallback"))),
		ui.Text(" Static separator "),
		ui.RequestRegion("cart-hole", "cart", []int{1, 2}, ui.Text("Public cart fallback")),
		ui.Text(" Static suffix")))
	if err != nil {
		t.Fatal(err)
	}
	value.HTML, value.Regions = shell.HTML, shell.Regions
	return value
}

func TestSharedPageSnapshotPartialShellRoundTrip(t *testing.T) {
	for _, prerendered := range []bool{false, true} {
		name := "runtime"
		if prerendered {
			name = "build"
		}
		t.Run(name, func(t *testing.T) {
			value := sharedPartialCodecFixture(t)
			value.Prerendered = prerendered
			dto, err := encodeSharedPage(value, "partial-binding")
			if err != nil {
				t.Fatal(err)
			}
			if dto.Format != 3 || dto.Prerendered != prerendered || len(dto.Regions) != 2 {
				t.Fatal(dto)
			}
			data, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("headHTML")) {
				t.Fatal("partial codec persisted raw head markup")
			}
			var transferred sharedPageSnapshot
			if err := json.Unmarshal(data, &transferred); err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeSharedPage(transferred, "partial-binding")
			if err != nil {
				t.Fatal(err)
			}
			if decoded.HTML != value.HTML || !reflect.DeepEqual(decoded.Regions, value.Regions) || decoded.Prerendered != prerendered || !decoded.GeneratedAt.Equal(value.GeneratedAt) {
				t.Fatal("partial snapshot changed shell, descriptors, provenance, or age", decoded)
			}
			if decoded.Page.Body != nil || decoded.Page.Stream || decoded.Page.preparedHead.markup != value.Page.preparedHead.markup || *decoded.Page.document != *value.Page.document {
				t.Fatal("partial snapshot retained live rendering or lost head/document")
			}
			if err := ui.ValidateShell(ui.CapturedShell{HTML: decoded.HTML, Regions: decoded.Regions}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSharedPageSnapshotPartialRegionsOwnContainersAndProps(t *testing.T) {
	value := sharedPartialCodecFixture(t)
	dto, err := encodeSharedPage(value, "binding")
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(dto.Regions[0].Props)
	value.Regions[0].Props[0] = '!'
	value.Regions[0].Region = "changed-source"
	value.Regions[0].Start++
	if !bytes.Equal(dto.Regions[0].Props, original) || dto.Regions[0].Region != "account" || dto.Regions[0].Start == value.Regions[0].Start {
		t.Fatal("encoded descriptors alias the source")
	}
	decoded, err := decodeSharedPage(dto, "binding")
	if err != nil {
		t.Fatal(err)
	}
	dto.Regions[0].Props[0] = '!'
	dto.Regions[0].Region = "changed-dto"
	dto.Regions[0].Start++
	if !bytes.Equal(decoded.Regions[0].Props, original) || decoded.Regions[0].Region != "account" || decoded.Regions[0].Start == dto.Regions[0].Start {
		t.Fatal("decoded descriptors alias the DTO")
	}
}

func TestSharedPageSnapshotPartialFormatContract(t *testing.T) {
	for _, test := range []struct {
		name        string
		format      int
		prerendered bool
		regions     int
		want        bool
	}{
		{"full-runtime", 1, false, 0, true}, {"full-build", 2, true, 0, true},
		{"runtime-claims-build", 1, true, 0, false}, {"build-loses-provenance", 2, false, 0, false},
		{"legacy-runtime-regions", 1, false, 1, false}, {"legacy-build-regions", 2, true, 1, false},
		{"legacy-runtime-empty-field", 1, false, -1, false}, {"legacy-build-empty-field", 2, true, -1, false},
		{"partial-runtime", 3, false, 1, true}, {"partial-build", 3, true, 1, true},
		{"partial-nil", 3, false, 0, false}, {"partial-empty", 3, true, -1, false}, {"future", 4, false, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := sharedPartialCodecFixture(t)
			dto, err := encodeSharedPage(value, "binding")
			if err != nil {
				t.Fatal(err)
			}
			dto.Format, dto.Prerendered = test.format, test.prerendered
			if test.regions == 0 {
				dto.Regions = nil
			} else if test.regions < 0 {
				dto.Regions = []ui.RegionSpec{}
			}
			_, err = decodeSharedPage(dto, "binding")
			if (err == nil) != test.want {
				t.Fatalf("format%d provenance%v descriptors%d: %v", dto.Format, dto.Prerendered, len(dto.Regions), err)
			}
		})
	}
	value := sharedPartialCodecFixture(t)
	value.Page.Stream = true
	if _, err := encodeSharedPage(value, "binding"); !errors.Is(err, ErrSharedPageSnapshot) {
		t.Fatal("live stream entered partial snapshot", err)
	}
}

func TestSharedPageSnapshotRejectsMalformedPartialShell(t *testing.T) {
	changes := map[string]func(*sharedPageSnapshot){
		"missing-regions":      func(v *sharedPageSnapshot) { v.Regions = nil },
		"region-count":         func(v *sharedPageSnapshot) { v.Regions = make([]ui.RegionSpec, ui.MaxShellRegions+1) },
		"empty-regions":        func(v *sharedPageSnapshot) { v.Regions = []ui.RegionSpec{} },
		"empty-id":             func(v *sharedPageSnapshot) { v.Regions[0].ID = "" },
		"invalid-id":           func(v *sharedPageSnapshot) { v.Regions[0].ID = "not an id" },
		"long-id":              func(v *sharedPageSnapshot) { v.Regions[0].ID = strings.Repeat("a", 129) },
		"duplicate-id":         func(v *sharedPageSnapshot) { v.Regions[1].ID = v.Regions[0].ID },
		"empty-registration":   func(v *sharedPageSnapshot) { v.Regions[0].Region = "" },
		"invalid-registration": func(v *sharedPageSnapshot) { v.Regions[0].Region = "server/handler" },
		"long-registration":    func(v *sharedPageSnapshot) { v.Regions[0].Region = strings.Repeat("a", 129) },
		"start-negative":       func(v *sharedPageSnapshot) { v.Regions[0].Start = -1 },
		"end-negative":         func(v *sharedPageSnapshot) { v.Regions[0].End = -1 },
		"start-overflow":       func(v *sharedPageSnapshot) { v.Regions[0].Start = int(^uint(0) >> 1) },
		"end-overflow":         func(v *sharedPageSnapshot) { v.Regions[0].End = int(^uint(0) >> 1) },
		"end-outside":          func(v *sharedPageSnapshot) { v.Regions[0].End = len(v.HTML) + 1 },
		"reversed-range":       func(v *sharedPageSnapshot) { v.Regions[0].Start = v.Regions[0].End + 1 },
		"overlap":              func(v *sharedPageSnapshot) { v.Regions[1].Start = v.Regions[0].End - 1 },
		"reversed-order":       func(v *sharedPageSnapshot) { v.Regions[0], v.Regions[1] = v.Regions[1], v.Regions[0] },
		"start-not-header":     func(v *sharedPageSnapshot) { v.Regions[0].Start++ },
		"end-not-footer":       func(v *sharedPageSnapshot) { v.Regions[0].End-- },
		"utf8-offset":          func(v *sharedPageSnapshot) { v.Regions[0].Start = strings.Index(v.HTML, "☃") + 1 },
		"damaged-header": func(v *sharedPageSnapshot) {
			raw := []byte(v.HTML)
			raw[v.Regions[0].Start] = '?'
			v.HTML = string(raw)
		},
		"damaged-footer": func(v *sharedPageSnapshot) {
			raw := []byte(v.HTML)
			raw[v.Regions[0].End-1] = 'x'
			v.HTML = string(raw)
		},
		"nil-props":       func(v *sharedPageSnapshot) { v.Regions[0].Props = nil },
		"empty-props":     func(v *sharedPageSnapshot) { v.Regions[0].Props = json.RawMessage{} },
		"invalid-props":   func(v *sharedPageSnapshot) { v.Regions[0].Props = json.RawMessage(`{`) },
		"props-duplicate": func(v *sharedPageSnapshot) { v.Regions[0].Props = json.RawMessage(`{"k":1,"k":2}`) },
		"props-trailing":  func(v *sharedPageSnapshot) { v.Regions[0].Props = json.RawMessage(`{}[]`) },
		"props-utf8":      func(v *sharedPageSnapshot) { v.Regions[0].Props = json.RawMessage{'"', 255, '"'} },
		"props-depth": func(v *sharedPageSnapshot) {
			v.Regions[0].Props = json.RawMessage(strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66))
		},
		"props-size": func(v *sharedPageSnapshot) {
			v.Regions[0].Props = json.RawMessage(`"` + strings.Repeat("x", ui.MaxRegionPropsBytes) + `"`)
		},
		"shell-size": func(v *sharedPageSnapshot) { v.HTML += strings.Repeat("x", ui.MaxShellBytes-len(v.HTML)+1) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			dto, err := encodeSharedPage(sharedPartialCodecFixture(t), "binding")
			if err != nil {
				t.Fatal(err)
			}
			change(&dto)
			if _, err := decodeSharedPage(dto, "binding"); !errors.Is(err, ErrSharedPageSnapshot) {
				t.Fatal("malformed partial snapshot accepted", err)
			}
		})
	}
	// Aggregate props admission is stricter than validating any single region.
	value := sharedCodecFixture(t)
	nodes := make([]ui.Node, 17)
	for i := range nodes {
		nodes[i] = ui.RequestRegion("hole"+strconv.Itoa(i), "region", nil, ui.Text("fallback"))
	}
	shell, err := ui.CaptureShell(context.Background(), ui.Fragment(nodes...))
	if err != nil {
		t.Fatal(err)
	}
	value.HTML, value.Regions = shell.HTML, shell.Regions
	for i := range value.Regions {
		value.Regions[i].Props = json.RawMessage(`"` + strings.Repeat("x", ui.MaxRegionPropsBytes-2) + `"`)
	}
	if _, err := encodeSharedPage(value, "binding"); !errors.Is(err, ErrSharedPageSnapshot) {
		t.Fatal("aggregate props budget accepted", err)
	}
}

func TestSharedPageSnapshotPartialDetachesDescriptorBacking(t *testing.T) {
	for _, operation := range []string{"encode", "decode"} {
		t.Run(operation, func(t *testing.T) {
			names := "account-hole|account" + strings.Repeat("x", 4<<20)
			propsBacking := []byte(`{"public":true}` + strings.Repeat(" ", 4<<20))
			spec := ui.RegionSpec{ID: names[:len("account-hole")], Region: names[len("account-hole")+1 : len("account-hole|account")], Props: propsBacking[:len(`{"public":true}`)]}
			source := sharedPartialCodecFixture(t)
			spec.Start, spec.End = source.Regions[0].Start, source.Regions[0].End
			var owned ui.RegionSpec
			if operation == "encode" {
				source.Regions[0] = spec
				dto, err := encodeSharedPage(source, "binding")
				if err != nil {
					t.Fatal(err)
				}
				owned = dto.Regions[0]
			} else {
				dto, err := encodeSharedPage(source, "binding")
				if err != nil {
					t.Fatal(err)
				}
				dto.Regions[0] = spec
				value, err := decodeSharedPage(dto, "binding")
				if err != nil {
					t.Fatal(err)
				}
				owned = value.Regions[0]
			}
			base := uintptr(unsafe.Pointer(unsafe.StringData(names)))
			for _, name := range []string{owned.ID, owned.Region} {
				at := uintptr(unsafe.Pointer(unsafe.StringData(name)))
				if at >= base && at < base+uintptr(len(names)) {
					t.Fatal("codec descriptor retains oversized identity backing")
				}
			}
			propsBase := uintptr(unsafe.Pointer(unsafe.SliceData(propsBacking)))
			propsAt := uintptr(unsafe.Pointer(unsafe.SliceData(owned.Props)))
			if propsAt >= propsBase && propsAt < propsBase+uintptr(len(propsBacking)) {
				t.Fatal("codec descriptor retains oversized props backing")
			}
		})
	}
}
