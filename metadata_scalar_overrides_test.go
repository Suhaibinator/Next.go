package nextgo

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func metadataScalarClearAll() seo.ScalarClears {
	return seo.ScalarClears{Description: true, Canonical: true, Robots: true}
}

func metadataScalarParent() seo.Metadata {
	return seo.Metadata{Title: "Parent", Description: "inherited description", Canonical: "https://example.test/inherited", Robots: "index", OpenGraph: seo.OpenGraph{Description: "social survives"}}
}

func metadataScalarReadEnvelope(t *testing.T, app *App) Envelope {
	t.Helper()
	w := request(app, "/", true)
	var envelope Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != 200 || envelope.Status != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	return envelope
}

func metadataScalarAssertAbsent(t *testing.T, description string, elements []seo.Element) {
	t.Helper()
	if description != "" {
		t.Fatal("description resurrected", description)
	}
	for _, element := range elements {
		if element.Attrs["rel"] == "canonical" || element.Attrs["name"] == "robots" || element.Attrs["name"] == "description" {
			t.Fatal("cleared head element resurrected", element)
		}
	}
}

func TestMetadataScalarClearsThreeLayersAcrossSSRAndNavigation(t *testing.T) {
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Body: ui.Text("useful body")}, nil
	}, Metadata: []MetadataLayer{
		{Load: func(*Context, seo.Metadata) (seo.Metadata, error) { return metadataScalarParent(), nil }},
		{Load: func(_ *Context, parent seo.Metadata) (seo.Metadata, error) {
			if parent.Description != "inherited description" {
				t.Fatal(parent)
			}
			return seo.Metadata{Clear: metadataScalarClearAll()}, nil
		}},
		{Load: func(_ *Context, parent seo.Metadata) (seo.Metadata, error) {
			if parent.Clear != metadataScalarClearAll() || parent.Description != "" || parent.Canonical != "" || parent.Robots != "" {
				t.Fatal("provider snapshot lost clear", parent)
			}
			return seo.Metadata{}, nil
		}},
	}}}})
	envelope := metadataScalarReadEnvelope(t, app)
	metadataScalarAssertAbsent(t, envelope.Description, envelope.Head)
	if envelope.HTML != "useful body" || envelope.Title != "Parent" {
		t.Fatal(envelope)
	}
	w := request(app, "/", false)
	if strings.Count(w.Body.String(), `name="description"`) != 1 || strings.Count(w.Body.String(), `<meta name="description" content="">`) != 1 {
		t.Fatal("SSR must retain exactly one empty framework description anchor", w.Body.String())
	}
	for _, forbidden := range []string{`name="robots"`, `rel="canonical"`, "inherited description", "example.test/inherited"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatal("SSR retained cleared data", forbidden, w.Body.String())
		}
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "social survives") || !strings.Contains(w.Body.String(), "useful body") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMetadataScalarClearsKeepExplicitLegacyPrecedence(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(map[bool]string{false: "sync", true: "deferred"}[deferred], func(t *testing.T) {
			app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
				p := Page{Title: "Legacy title", Description: "Legacy description", Meta: map[string]string{"RoBoTs": "noindex"}, Body: ui.Text("body"), Metadata: seo.Metadata{Clear: metadataScalarClearAll()}}
				if deferred {
					p.DeferredMetadata = func(*Context, seo.Metadata) (seo.Metadata, error) {
						return seo.Metadata{Clear: metadataScalarClearAll()}, nil
					}
				}
				return p, nil
			}, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) { return metadataScalarParent(), nil }}}}}})
			e := metadataScalarReadEnvelope(t, app)
			if e.Title != "Legacy title" || e.Description != "Legacy description" || e.Meta["robots"] != "noindex" {
				t.Fatal(e)
			}
			for _, element := range e.Head {
				if element.Attrs["rel"] == "canonical" {
					t.Fatal("canonical not cleared", element)
				}
			}
		})
	}
}

func TestMetadataScalarClearsDoNotMaskInvalidLiteralReplacement(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
			m := seo.Metadata{Canonical: "javascript:private-diagnostic", Clear: metadataScalarClearAll()}
			p := Page{Body: ui.Text("must not render")}
			if deferred {
				p.DeferredMetadata = func(*Context, seo.Metadata) (seo.Metadata, error) { return m, nil }
			} else {
				p.Metadata = m
			}
			return p, nil
		}}}})
		w := request(app, "/", true)
		if w.Code != 500 || strings.Contains(w.Body.String(), "private-diagnostic") || strings.Contains(w.Body.String(), "must not render") {
			t.Fatal(deferred, w.Code, w.Body.String())
		}
	}
}

func TestMetadataScalarClearsCannotRemoveAccessNoindex(t *testing.T) {
	app := testApp(t, Config{Forbidden: func(*Context) (Page, error) {
		return Page{Metadata: seo.Metadata{Clear: metadataScalarClearAll()}, Body: ui.Text("access boundary")}, nil
	}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{}, ErrForbidden }}}})
	for _, nav := range []bool{false, true} {
		w := request(app, "/", nav)
		if w.Code != 403 || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Fatal(w.Code, w.Header())
		}
		if nav {
			var e Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if len(e.Head) != 1 || e.Head[0].Attrs["name"] != "robots" || e.Head[0].Attrs["content"] != "noindex" {
				t.Fatal(e.Head)
			}
		} else if !strings.Contains(w.Body.String(), `name="robots"`) || !strings.Contains(w.Body.String(), `content="noindex"`) {
			t.Fatal(w.Body.String())
		}
	}
}

func TestMetadataScalarClearsPublicCacheAndInvalidation(t *testing.T) {
	var calls atomic.Int32
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		calls.Add(1)
		return Page{Body: ui.Text("cached body"), Metadata: seo.Metadata{Clear: metadataScalarClearAll()}}, nil
	}, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) { return metadataScalarParent(), nil }}}}}})
	for i := 0; i < 2; i++ {
		e := metadataScalarReadEnvelope(t, app)
		metadataScalarAssertAbsent(t, e.Description, e.Head)
	}
	if calls.Load() != 1 {
		t.Fatal("snapshot not reused", calls.Load())
	}
	app.RevalidatePath("/")
	e := metadataScalarReadEnvelope(t, app)
	metadataScalarAssertAbsent(t, e.Description, e.Head)
	if calls.Load() != 2 {
		t.Fatal("invalidation did not reload", calls.Load())
	}
}

func TestMetadataScalarClearsMaterializedSharedAndPrerenderCodecs(t *testing.T) {
	m := seo.Merge(metadataScalarParent(), seo.Metadata{Clear: metadataScalarClearAll()})
	p := Page{Title: m.Title, Metadata: m}
	head, markup, err := pageHead(p)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pageDocument(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, prerendered := range []bool{false, true} {
		value := ownCachedPage(cachedPage{HTML: "public body", GeneratedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), Prerendered: prerendered, Page: Page{Title: p.Title, document: &doc, preparedHead: &renderedHead{elements: head, markup: markup}}})
		dto, err := encodeSharedPage(value, "scalar-binding")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		var transferred sharedPageSnapshot
		if err = json.Unmarshal(data, &transferred); err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeSharedPage(transferred, "scalar-binding")
		if err != nil {
			t.Fatal(err)
		}
		metadataScalarAssertAbsent(t, decoded.Page.Description, decoded.Page.preparedHead.elements)
		if decoded.HTML != "public body" || decoded.Prerendered != prerendered || !strings.Contains(decoded.Page.preparedHead.markup, "social survives") {
			t.Fatal(decoded)
		}
	}
}

func TestMetadataScalarClearsDeferredBlockingAndLegacyDescription(t *testing.T) {
	var calls atomic.Int32
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Body: ui.Text("stable body"), Metadata: metadataScalarParent(), DeferredMetadata: func(_ *Context, parent seo.Metadata) (seo.Metadata, error) {
			calls.Add(1)
			if parent.Description != "inherited description" {
				t.Fatal(parent)
			}
			return seo.Metadata{Title: "Final", Clear: metadataScalarClearAll()}, nil
		}}, nil
	}}}})
	e := metadataScalarReadEnvelope(t, app)
	metadataScalarAssertAbsent(t, e.Description, e.Head)
	if e.Title != "Final" || e.HTML != "stable body" || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
	w := request(app, "/", false)
	if w.Code != 200 || calls.Load() != 2 || strings.Count(w.Body.String(), `name="description"`) != 1 || strings.Count(w.Body.String(), `<meta name="description" content="">`) != 1 || strings.Contains(w.Body.String(), "inherited description") || strings.Contains(w.Body.String(), "example.test/inherited") || strings.Contains(w.Body.String(), `rel="canonical"`) || strings.Contains(w.Body.String(), `name="robots"`) {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
}

func TestMetadataScalarClearsNegotiatedFinalKeepsResourceProvenance(t *testing.T) {
	var resources atomic.Int32
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Text("stable body"), Metadata: metadataScalarParent(), Resources: []assets.Resource{metadataResource{calls: &resources}}, DeferredMetadata: func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Title: "Final", Clear: metadataScalarClearAll()}, nil
		}}, nil
	}}}})
	w := deferredTestRequest(app, "GET", metadataBrowserUA, true, true, []string{"1"})
	if w.Code != 200 || resources.Load() != 1 {
		t.Fatal(w.Code, resources.Load(), w.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatal(w.Body.String())
	}
	var shell navigation.StreamFrame
	if err := json.Unmarshal([]byte(lines[0]), &shell); err != nil || shell.Type != "shell" || !shell.MetadataPending {
		t.Fatal(shell, err)
	}
	var initial Envelope
	if err := json.Unmarshal(shell.Page, &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Description != "inherited description" {
		t.Fatal("missing meaningful fallback", initial)
	}
	for _, index := range shell.MetadataIndices {
		if index < 0 || index >= len(initial.Head) || initial.Head[index].Attrs["rel"] == "stylesheet" {
			t.Fatal("resource entered SEO ownership", index, initial.Head)
		}
	}
	var final navigation.StreamFrame
	if err := json.Unmarshal([]byte(lines[1]), &final); err != nil || final.Type != "metadata" || final.Failed {
		t.Fatal(final, err)
	}
	payload, err := navigation.DecodeMetadataPayload(final.Metadata, false)
	if err != nil || payload.Owner != shell.MetadataOwner || payload.Title != "Final" || payload.Description != "" {
		t.Fatal(payload, err)
	}
	if len(payload.Elements) != 1 || payload.Elements[0].Attrs["property"] != "og:description" {
		t.Fatal(payload.Elements)
	}
	if !strings.Contains(lines[2], `"type":"done"`) {
		t.Fatal(lines[2])
	}
}
