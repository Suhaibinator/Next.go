package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestConfigurationPhaseDefaultsAndValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
		want   Phase
	}{
		{"custom production", Config{}, PhaseProduction},
		{"legacy development", Config{Dev: true}, PhaseDevelopment},
		{"legacy prerender", Config{PrerenderBuildMode: true}, PhasePrerender},
		{"explicit production", Config{Phase: PhaseProduction}, PhaseProduction},
		{"explicit export", Config{Phase: PhaseStaticExport}, PhaseStaticExport},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := testApp(t, test.config)
			defer app.Close(context.Background())
			if app.config.Phase != test.want {
				t.Fatal(app.config.Phase, test.want)
			}
		})
	}
	for _, config := range []Config{{Phase: "unknown"}, {Phase: PhaseDevelopment}, {Phase: PhasePrerender}, {Phase: PhaseProduction, Dev: true}, {Phase: PhaseStaticExport, Dev: true}, {Phase: PhaseStaticExport, PrerenderBuildMode: true}, {Dev: true, PrerenderBuildMode: true}} {
		if app, err := New(config); err == nil {
			app.Close(context.Background())
			t.Fatal("invalid phase/mode accepted", config.Phase)
		}
	}
	// Phase rejection precedes validation/allocation of other native resources.
	if _, err := New(Config{Phase: "unknown", EarlyScripts: []scripts.Script{{ID: "invalid"}}}); err == nil || !strings.Contains(err.Error(), "unknown configuration phase") {
		t.Fatal("phase validation did not run first", err)
	}
}

type phaseObservedFS struct {
	fs.FS
	opens *atomic.Int32
}

func (f phaseObservedFS) Open(name string) (fs.File, error) { f.opens.Add(1); return f.FS.Open(name) }

func TestConfigurationPhaseExportIsolatesSnapshotsAndPreservesFallback(t *testing.T) {
	var loads, opens atomic.Int32
	value := "production snapshot"
	route := Route{Pattern: "/items/:slug", StaticParams: func(context.Context) ([]Params, error) { return []Params{{"slug": "known"}}, nil }, Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text(value)}, nil }}
	c, data := buildPrerenderTest(t, Config{Cache: cache.New(32), Routes: []Route{route}})
	c.PrerenderFS = phaseObservedFS{fstest.MapFS{"prerender.json": {Data: data}}, &opens}
	production := testApp(t, c)
	defer production.Close(context.Background())
	value = "fresh export"
	exportConfig := c
	exportConfig.Phase = PhaseStaticExport
	export := testApp(t, exportConfig)
	defer export.Close(context.Background())
	if opens.Load() != 1 {
		t.Fatal("export read production private artifact", opens.Load())
	}
	if production.prerenderShape == export.prerenderShape {
		t.Fatal("export did not separate its snapshot shape")
	}
	if production.configurationID == export.configurationID || production.prerenderKey("/items/known") == export.prerenderKey("/items/known") {
		t.Fatal("export reused production cache namespace")
	}
	w, done := prerenderRead(export, "/items/known", nil)
	out := finishPrerender(t, w, done)
	if w.Code != 200 || out.HTML != "fresh export" || loads.Load() != 2 {
		t.Fatal("export reused production snapshot", w.Code, out.HTML, loads.Load())
	}
	w, done = prerenderRead(export, "/items/missing", nil)
	finishPrerender(t, w, done)
	if w.Code != 404 || loads.Load() != 2 {
		t.Fatal("export weakened fallback membership", w.Code, loads.Load())
	}
	w, done = prerenderRead(production, "/items/known", nil)
	out = finishPrerender(t, w, done)
	if out.HTML != "production snapshot" || loads.Load() != 2 {
		t.Fatal("export changed production cache", out.HTML, loads.Load())
	}
	if strings.Contains(export.navigationJSON, "static-export") || strings.Contains(export.navigationJSON, `"phase"`) {
		t.Fatal("native phase was published as runtime configuration", export.navigationJSON)
	}
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(export.navigationJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if expected := navigation.ConfigurationID(export.config.BuildID, export.paths, manifest, export.config.PreserveLayouts, export.config.ViewTransitions); expected != export.configurationID {
		t.Fatal("browser cannot validate exported configuration", expected, export.configurationID)
	}
}

func TestConfigurationPhaseProductionProbePreservesSnapshotIdentity(t *testing.T) {
	production := testApp(t, Config{BuildID: "same-source"})
	defer production.Close(context.Background())
	probe := testApp(t, Config{Phase: PhasePrerender, PrerenderBuildMode: true, BuildID: "same-source"})
	defer probe.Close(context.Background())
	if production.prerenderShape != probe.prerenderShape || production.configurationID != probe.configurationID {
		t.Fatal("prerender lifecycle changed production snapshot ownership")
	}
}

func TestConfigurationPhaseExportDoesNotConsumeProductionSharedSeeds(t *testing.T) {
	backend, store := sharedPrerenderMemory(t)
	value := "production build"
	route := sharedPrerenderRoute(func(*Context) (Page, error) { return Page{Body: ui.Text(value)}, nil })
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	value = "export runtime"
	exportConfig := c
	exportConfig.Phase = PhaseStaticExport
	export := sharedPageApp(t, exportConfig)
	w := sharedPageRead(t, export, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "export runtime" || backend.sessions.Load() != 0 {
		t.Fatal("export imported/consumed production shared seeds", w.Code, w.Body.String(), backend.sessions.Load())
	}
	production := sharedPageApp(t, c)
	w = sharedPageRead(t, production, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "production build" || backend.sessions.Load() != 1 {
		t.Fatal("production ticket was not preserved", w.Code, w.Body.String(), backend.sessions.Load())
	}
	if export.sharedPrerenderBinding(export.routes[0], "/") == production.sharedPrerenderBinding(production.routes[0], "/") {
		t.Fatal("shared bindings mixed native/export ownership")
	}
}

func TestConfigurationPhaseDoesNotRelaxStaticRefusals(t *testing.T) {
	app := testApp(t, Config{Phase: PhaseStaticExport, HeaderRules: []HeaderRule{{Source: "/", Headers: []HeaderValue{{Name: "Cache-Control", Value: "no-cache"}}}}})
	defer app.Close(context.Background())
	if _, err := app.StaticExportConfig(); err == nil || !strings.Contains(err.Error(), "response header rules") {
		t.Fatal("phase silently removed native policy", err)
	}
}

func TestConfigurationPhaseExportMembershipHonorsStartupCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewContext(ctx, Config{Phase: PhaseStaticExport, Routes: []Route{{Pattern: "/:slug", StaticParams: func(ctx context.Context) ([]Params, error) { <-ctx.Done(); return nil, ctx.Err() }, Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, Page: staticPage}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("export membership ignored startup cancellation", err)
	}
}
