package nextgo

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaticParamsCompositionAndSharedDeclarations(t *testing.T) {
	var parents, children int
	parent := func(ctx context.Context) ([]Params, error) {
		parents++
		if len(StaticParentParams(ctx)) != 0 {
			t.Error("root inherited parameters")
		}
		return []Params{{"category": "coffee"}, {"category": "tea"}}, nil
	}
	layer := StaticParamsLayer{ID: "catalog", Pattern: "/catalog/:category", Load: parent}
	child := func(ctx context.Context) ([]Params, error) {
		children++
		p := StaticParentParams(ctx)
		category := p["category"]
		p["category"] = "mutated"
		if StaticParentParams(ctx)["category"] != category {
			t.Error("parent snapshot is not owned")
		}
		if category == "tea" {
			return []Params{{"item": "green"}}, nil
		}
		return []Params{{"item": "espresso"}, {"category": category, "item": "filter"}}, nil
	}
	app := testApp(t, Config{Routes: []Route{
		{Pattern: "/catalog/:category", Page: staticPage, StaticParams: parent, StaticParamsID: "catalog"},
		{Pattern: "/catalog/:category/summary", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{layer}},
		{Pattern: "/catalog/:category/items/:item", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{layer}, StaticParams: child},
		{Pattern: "/catalog/:category/missing/:item", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{layer}},
	}})
	paths, err := app.StaticPaths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/catalog/coffee", "/catalog/coffee/items/espresso", "/catalog/coffee/items/filter", "/catalog/coffee/summary", "/catalog/tea", "/catalog/tea/items/green", "/catalog/tea/summary"}
	if !reflect.DeepEqual(paths, want) || parents != 1 || children != 2 {
		t.Fatal(paths, parents, children)
	}
	if _, err := app.StaticPaths(context.Background()); err != nil || parents != 2 || children != 4 {
		t.Fatal("enumeration cache leaked across calls", err, parents, children)
	}
}
func TestStaticParamsCompositionRejectsInvalidBindings(t *testing.T) {
	for name, rows := range map[string][]Params{"overwrite": {{"category": "other", "item": "x"}}, "unknown": {{"item": "x", "other": "y"}}, "missing": {nil}, "unsafe": {{"item": "../x"}}} {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, Config{Routes: []Route{{Pattern: "/:category/:item", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "parent", Pattern: "/:category", Load: func(context.Context) ([]Params, error) { return []Params{{"category": "public"}}, nil }}}, StaticParams: func(context.Context) ([]Params, error) { return rows, nil }}}})
			if _, err := app.StaticPaths(context.Background()); err == nil {
				t.Fatal("invalid child accepted")
			}
		})
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/:category/:item", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "parent", Pattern: "/:category", Load: func(context.Context) ([]Params, error) {
		return []Params{{"category": "public", "item": "below-prefix"}}, nil
	}}}, StaticParams: func(context.Context) ([]Params, error) { t.Fatal("invalid parent reached child"); return nil, nil }}}})
	if _, err := app.StaticPaths(context.Background()); err == nil {
		t.Fatal("parent bound descendant")
	}
}
func TestStaticParamsCompositionCatchallsAndEmptyBranches(t *testing.T) {
	parent := StaticParamsLayer{ID: "parent", Pattern: "/docs/:lang", Load: func(context.Context) ([]Params, error) { return []Params{{"lang": "fr"}, {"lang": "en"}}, nil }}
	app := testApp(t, Config{Routes: []Route{
		{Pattern: "/docs/:lang/guide/*tail?", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{parent}, StaticParams: func(ctx context.Context) ([]Params, error) {
			if StaticParentParams(ctx)["lang"] == "en" {
				return nil, nil
			}
			return []Params{nil, {"tail": "café/Go WASM"}}, nil
		}},
		{Pattern: "/docs/:lang/default/*tail?", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{parent}},
	}})
	paths, err := app.StaticPaths(context.Background())
	want := []string{"/docs/en/default", "/docs/fr/default", "/docs/fr/guide", "/docs/fr/guide/caf%C3%A9/Go%20WASM"}
	if err != nil || !reflect.DeepEqual(paths, want) {
		t.Fatal(paths, err)
	}
}
func TestStaticParamsLayerValidationAndOwnership(t *testing.T) {
	load := func(context.Context) ([]Params, error) { return []Params{{"x": "x"}}, nil }
	for _, layer := range []StaticParamsLayer{{Pattern: "/:x", Load: load}, {ID: "p", Pattern: "/:x"}, {ID: "p", Pattern: "/wrong/:x", Load: load}, {ID: "p", Pattern: "/", Load: load}, {ID: "bad\x00", Pattern: "/:x", Load: load}} {
		if _, err := New(Config{Routes: []Route{{Pattern: "/:x/:y", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{layer}}}}); err == nil {
			t.Fatal("invalid layer accepted", layer)
		}
	}
	for _, route := range []Route{{Pattern: "/static", Page: staticPage}, {Pattern: "/:x", API: func(http.ResponseWriter, *http.Request) {}}} {
		route.StaticParamsLayers = []StaticParamsLayer{{ID: "p", Pattern: "/:x", Load: load}}
		if _, err := New(Config{Routes: []Route{route}}); err == nil {
			t.Fatal("invalid owner accepted")
		}
	}
	layers := []StaticParamsLayer{{ID: "p", Pattern: "/:x", Load: load}}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/:x/summary", Page: staticPage, StaticParamsLayers: layers}}})
	layers[0].Load = nil
	paths, err := app.StaticPaths(context.Background())
	if err != nil || !reflect.DeepEqual(paths, []string{"/x/summary"}) {
		t.Fatal("caller changed frozen layer", paths, err)
	}
	if StaticParentParams(nil) != nil || StaticParentParams(context.Background()) != nil {
		t.Fatal("parent scope escaped enumeration")
	}
}
func TestStaticParamsCompositionBoundsCancellationAndScope(t *testing.T) {
	var calls atomic.Int64
	app := testApp(t, Config{Routes: []Route{{Pattern: "/:x/:y", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "p", Pattern: "/:x", Load: func(context.Context) ([]Params, error) {
		rows := make([]Params, 100)
		for i := range rows {
			rows[i] = Params{"x": strings.Repeat("x", i+1)}
		}
		return rows, nil
	}}}, StaticParams: func(context.Context) ([]Params, error) {
		calls.Add(1)
		rows := make([]Params, 100)
		for i := range rows {
			rows[i] = Params{"y": "y"}
		}
		return rows, nil
	}}}})
	if _, err := app.StaticPaths(context.Background()); err == nil || calls.Load() > 100 {
		t.Fatal("unbounded generator expansion", err, calls.Load())
	}
	scoped := testApp(t, Config{Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Routes: []Route{{Pattern: "/:lang/summary", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "language", Pattern: "/:lang", Load: func(ctx context.Context) ([]Params, error) { return []Params{{"lang": CurrentLocale(ctx)}}, nil }}}}}})
	paths, err := scoped.StaticPaths(context.Background())
	if err != nil || !reflect.DeepEqual(paths, []string{"/en/en/summary", "/fr/fr/summary"}) {
		t.Fatal("locale enumeration shared private input", paths, err)
	}
	canceled := testApp(t, Config{Routes: []Route{{Pattern: "/:x/:y", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "p", Pattern: "/:x", Load: func(ctx context.Context) ([]Params, error) { <-ctx.Done(); return nil, ctx.Err() }}}, StaticParams: func(context.Context) ([]Params, error) { return nil, nil }}}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := canceled.StaticPaths(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestStaticParamsNestedCompositionAndDeclarationConflicts(t *testing.T) {
	root := func(context.Context) ([]Params, error) { return []Params{{"a": "one"}}, nil }
	middle := func(ctx context.Context) ([]Params, error) {
		if StaticParentParams(ctx)["a"] != "one" {
			return nil, errors.New("missing grandparent")
		}
		return []Params{{"b": "two"}}, nil
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/:a/:b/:c", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "root", Pattern: "/:a", Load: root}, {ID: "middle", Pattern: "/:a/:b", Load: middle}}, StaticParams: func(ctx context.Context) ([]Params, error) {
		if !reflect.DeepEqual(StaticParentParams(ctx), Params{"a": "one", "b": "two"}) {
			return nil, errors.New("missing ancestor")
		}
		return []Params{{"c": "three"}}, nil
	}}}})
	values, err := app.StaticPaths(context.Background())
	if err != nil || !reflect.DeepEqual(values, []string{"/one/two/three"}) {
		t.Fatal(values, err)
	}
	if _, err := New(Config{Routes: []Route{{Pattern: "/:a/one", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "same", Pattern: "/:a", Load: root}}}, {Pattern: "/:a/two", Page: staticPage, StaticParamsLayers: []StaticParamsLayer{{ID: "same", Pattern: "/:a", Load: middle}}}}}); err == nil {
		t.Fatal("conflicting declaration identity accepted")
	}
}
