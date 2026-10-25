//go:build !js

package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestSSRInstancesAndStaticControlsAreCoherent(t *testing.T) {
	t.Setenv("NEXTGO_REFRESH_BASE_PATH", "/refresh-demo")
	t.Setenv("NEXTGO_REFRESH_PUBLIC_NOTE", "Public native fixture")
	for _, phase := range []nextgo.Phase{nextgo.PhaseProduction, nextgo.PhaseStaticExport} {
		c := nextgo.Config{Phase: phase, BuildID: "refresh-ssr", Routes: []nextgo.Route{{Pattern: "/", Page: Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"refresh-layout"}}}}
		Configure(&c)
		a, err := nextgo.New(c)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://example.test/refresh-demo", nil))
		if w.Code != 200 {
			t.Fatal(phase, w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, want := range []string{`data-refresh-instance="primary"`, `data-refresh-instance="secondary"`, `data-refresh-count="">3`, `data-refresh-count="">10`, `data-refresh-eager=""`, `href="/refresh-demo/details"`, `Public native fixture`} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %s", phase, want)
			}
		}
		if phase == nextgo.PhaseStaticExport {
			for _, unavailable := range []string{`data-refresh-work=""`, `data-refresh-server-instance`, `/api/status`, `/api/work`} {
				if strings.Contains(body, unavailable) {
					t.Fatalf("static output advertised unavailable %s", unavailable)
				}
			}
		} else if !strings.Contains(body, `data-refresh-work=""`) || !strings.Contains(body, `data-refresh-server-instance`) {
			t.Fatal("native diagnostics/work missing")
		}
		if err := a.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
