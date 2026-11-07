//go:build !js

package app

import (
	"context"
	"encoding/json"
	"html"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestMergeSampleUsesRealFreshLoadersAndCompleteServerEnvelope(t *testing.T) {
	cfg := nextgo.Config{BuildID: "merge-test", Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
	Configure(&cfg)
	app, err := nextgo.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	request := func(target string) nextgo.Envelope {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("X-Nextgo-Navigate", "1")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		var value nextgo.Envelope
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return value
	}
	a, b := request("/?q=a"), request("/?q=b")
	if a.Merge == nil || b.Merge == nil || len(b.Merge.Islands) != 2 || b.Merge.Validate(b.HTML) != nil || a.Title == b.Title || a.Description == b.Description || len(b.Head) == 0 {
		t.Fatal("missing actual fresh server metadata", a, b)
	}
	for _, want := range []string{`data-merge-panel="A"`, `data-merge-panel="B"`, `data-merge-query="">b`, `data-merge-price="">11.00`, `data-merge-price="">16.00`, `data-nextgo-key="A"`, `data-nextgo-key="B"`, `Significant server text`, `data-generation=`} {
		if !strings.Contains(b.HTML, want) {
			t.Fatal("fresh fallback/skeleton missing", want)
		}
	}
	for i := range a.Merge.Islands {
		before, _ := a.Merge.Address(a.Merge.Islands[i])
		after, _ := b.Merge.Address(b.Merge.Islands[i])
		if before != after {
			t.Fatal("query reset public component identity")
		}
	}
	repeated := request("/?q=b")
	if repeated.Merge.Islands[0].PropsSHA256 != b.Merge.Islands[0].PropsSHA256 || repeated.Merge.BodySHA256 == b.Merge.BodySHA256 {
		t.Fatal("same JSON Refresh fixture must still fresh-render surrounding server generation")
	}
}
func TestMergeSamplePublicControlsAreBoundedAndDenialIsFresh(t *testing.T) {
	cfg := nextgo.Config{Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
	Configure(&cfg)
	app, err := nextgo.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	control := httptest.NewRequest("POST", "/api/control", strings.NewReader(`{"version":"2","deny":true}`))
	result := httptest.NewRecorder()
	app.ServeHTTP(result, control)
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	r := httptest.NewRequest("GET", "/?q=b", nil)
	for _, cookie := range result.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 403 || strings.Contains(w.Body.String(), "data-merge-panel") {
		t.Fatal("denial leaked prior app", w.Code, w.Body.String())
	}
	bad := httptest.NewRecorder()
	app.ServeHTTP(bad, httptest.NewRequest("POST", "/api/control", strings.NewReader(strings.Repeat("x", 1025))))
	if bad.Code != 400 {
		t.Fatal(bad.Code)
	}
}

func TestMergeSampleOptionalOrdinaryFileAndRealEffectControls(t *testing.T) {
	cfg := nextgo.Config{BuildID: "merge-test", Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
	Configure(&cfg)
	app, err := nextgo.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	changed := httptest.NewRecorder()
	app.ServeHTTP(changed, httptest.NewRequest("POST", "/api/control", strings.NewReader(`{"navigate":"C"}`)))
	if changed.Code != 200 {
		t.Fatal(changed.Code, changed.Body.String())
	}
	r := httptest.NewRequest("GET", "/?q=b&q=&q=last&ordinary=1", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	for _, cookie := range changed.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var value nextgo.Envelope
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Merge == nil || len(value.Merge.Islands) != 2 || value.Merge.Validate(value.HTML) != nil {
		t.Fatal("real sample envelope", w.Code, w.Body.String())
	}
	decoded := html.UnescapeString(value.HTML)
	for _, want := range []string{`type="file"`, `data-merge-kind="fallback"`, `data-merge-ordinary=""`, `Ordinary fresh version 1 query b`, `q=b&q=&q=last&ordinary=1`, `"navigateOnUpdate":true`} {
		if !strings.Contains(decoded, want) {
			t.Fatal("missing real fixture behavior", want)
		}
	}
	if strings.Count(decoded, `"navigateOnUpdate":true`) != 1 {
		t.Fatal("only A may start the once-per-Scope later-revision navigation")
	}
	for _, body := range []string{`{"navigate":"elsewhere"}`, `{"navigate":"C","extra":1}`, `{"navigate":"C"} {}`, `{"version":"3"}`} {
		bad := httptest.NewRecorder()
		app.ServeHTTP(bad, httptest.NewRequest("POST", "/api/control", strings.NewReader(body)))
		if bad.Code != 400 || len(bad.Result().Cookies()) != 0 {
			t.Fatal("invalid fixture control admitted", body, bad.Code)
		}
	}
}
