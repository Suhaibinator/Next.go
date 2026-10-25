package nextgo_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/mutation"
	"github.com/Suhaibinator/Next.go/ui"
)

// These fixtures use only public packages and observable HTTP metadata. They
// model retained documents across upgrade/rollback without importing internal
// identity algorithms or making a published-version compatibility claim.
type compatibilityDocument struct {
	nextgo.Envelope
	draft string
}

func compatibilityApp(t *testing.T, build string, transitions bool, writes *atomic.Int64, revisions ...string) *nextgo.App {
	t.Helper()
	app, err := nextgo.New(nextgo.Config{
		BuildID: build, ViewTransitions: transitions,
		Logger: log.New(io.Discard, "", 0),
		Routes: []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Title: "Compatibility fixture", Body: ui.Text("revisioned page")}, nil
		}}},
		ActionRegistrars: []func(*actions.Router){func(router *actions.Router) {
			for _, revision := range revisions {
				contract := mutation.Must[struct{}, string]("compatibility/write", revision)
				contract.Bind(router, func(*actions.Context, struct{}) (mutation.Result[string], error) {
					writes.Add(1)
					return mutation.Result[string]{Data: revision}, nil
				})
			}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return app
}

func compatibilityRead(t *testing.T, app *nextgo.App) compatibilityDocument {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://compatibility.test/", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var document compatibilityDocument
	if err := json.Unmarshal(w.Body.Bytes(), &document.Envelope); err != nil || w.Code != http.StatusOK {
		t.Fatalf("navigation: status=%d body=%s error=%v", w.Code, w.Body.String(), err)
	}
	if document.Version != nextgo.NavigationProtocolVersion || w.Header().Get("X-Nextgo-Protocol") != "3" || document.Version != 3 {
		t.Fatalf("navigation public protocol/header changed: %+v headers=%v", document, w.Header())
	}
	if len(document.Configuration) != 64 || w.Header().Get("X-Nextgo-Configuration") != document.Configuration || document.Build == "" {
		t.Fatalf("missing or inconsistent generation: %+v headers=%v", document, w.Header())
	}
	if document.Document.Language == "" || document.Document.Direction == "" || document.Document.Viewport == "" {
		t.Fatal("version-three document metadata missing", document.Document)
	}
	document.draft = w.Header().Get("X-Nextgo-Draft")
	return document
}

func compatibilityPost(app *nextgo.App, revision, protocol, declaredRevision string, document *compatibilityDocument) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://compatibility.test/_nextgo/actions/compatibility/write/"+revision, strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://compatibility.test")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	if protocol != "" {
		r.Header.Set("X-Nextgo-Action-Protocol", protocol)
	}
	if declaredRevision != "" {
		r.Header.Set("X-Nextgo-Action-Revision", declaredRevision)
	}
	if document != nil {
		r.Header.Set("Accept", "application/x-nextgo-action-view+json")
		r.Header.Set("X-Nextgo-Action-View", "1")
		r.Header.Set("X-Nextgo-Action-URL", document.URL)
		r.Header.Set("X-Nextgo-Action-Build", document.Build)
		r.Header.Set("X-Nextgo-Configuration", document.Configuration)
		r.Header.Set("X-Nextgo-Draft", document.draft)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestCompatibilityOldDocumentsRejectWritesAcrossUpgradeAndRollback(t *testing.T) {
	var writes atomic.Int64
	oldApp := compatibilityApp(t, "old-code", false, &writes, "v1")
	newApp := compatibilityApp(t, "new-code", false, &writes, "v1")
	policyApp := compatibilityApp(t, "old-code", true, &writes, "v1")
	oldDocument := compatibilityRead(t, oldApp)
	newDocument := compatibilityRead(t, newApp)
	policyDocument := compatibilityRead(t, policyApp)
	if oldDocument.Configuration == newDocument.Configuration || oldDocument.Configuration == policyDocument.Configuration || oldDocument.Build != policyDocument.Build {
		t.Fatal("build or same-build runtime policy change did not separate ownership")
	}
	for _, test := range []struct {
		name     string
		app      *nextgo.App
		document compatibilityDocument
	}{
		{"old-document/new-build", newApp, oldDocument},
		{"new-document/rolled-back-build", oldApp, newDocument},
		{"old-document/new-runtime-policy", policyApp, oldDocument},
		{"new-policy-document/rolled-back-policy", oldApp, policyDocument},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := writes.Load()
			w := compatibilityPost(test.app, "v1", "1", "v1", &test.document)
			if w.Code != http.StatusConflict || writes.Load() != before || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatalf("incompatible document reached mutation: status=%d writes=%d body=%s", w.Code, writes.Load()-before, w.Body.String())
			}
			fresh := compatibilityRead(t, test.app)
			w = compatibilityPost(test.app, "v1", "1", "v1", &fresh)
			if w.Code != http.StatusOK || writes.Load() != before+1 || !strings.HasPrefix(w.Body.String(), `{"version":1,"type":"result","result":{"data":"v1"}}`+"\n") {
				t.Fatalf("fresh generation failed to confirm exactly one write: status=%d writes=%d body=%s", w.Code, writes.Load()-before, w.Body.String())
			}
		})
	}
}

func TestCompatibilityExplicitActionRevisionRetentionAndRemoval(t *testing.T) {
	var writes atomic.Int64
	retained := compatibilityApp(t, "retained", false, &writes, "v1", "v2")
	removed := compatibilityApp(t, "removed", false, &writes, "v2")
	for _, revision := range []string{"v1", "v2"} {
		w := compatibilityPost(retained, revision, "1", revision, nil)
		var result struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Data != revision || w.Header().Get("X-Nextgo-Action-Protocol") != "1" || w.Header().Get("X-Nextgo-Action-Revision") != revision {
			t.Fatalf("retained %s changed its meaning: status=%d body=%s error=%v", revision, w.Code, w.Body.String(), err)
		}
	}
	for _, test := range []struct {
		name, revision, protocol, declaredRevision string
		app                                        *nextgo.App
		status                                     int
	}{
		{"unsupported-protocol", "v1", "2", "v1", retained, http.StatusConflict},
		{"wrong-revision", "v1", "1", "v2", retained, http.StatusConflict},
		{"removed-revision", "v1", "1", "v1", removed, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := writes.Load()
			w := compatibilityPost(test.app, test.revision, test.protocol, test.declaredRevision, nil)
			if w.Code != test.status || writes.Load() != before {
				t.Fatalf("incompatible action reached mutation: status=%d writes=%d body=%s", w.Code, writes.Load()-before, w.Body.String())
			}
		})
	}
	// Progressive callers omit protocol headers but still choose an explicit URL.
	w := compatibilityPost(removed, "v2", "", "", nil)
	if w.Code != http.StatusOK || writes.Load() != 3 {
		t.Fatalf("explicit progressive revision failed: status=%d writes=%d body=%s", w.Code, writes.Load(), w.Body.String())
	}
}
