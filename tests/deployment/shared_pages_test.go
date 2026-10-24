//go:build deployment

package deployment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/seo"
)

var sharedPageGenerationRE = regexp.MustCompile(`data-shared-page-generation="">([a-f0-9]{32})</code>`)
var sharedPageMetaRE = regexp.MustCompile(`<meta\s[^>]*>`)
var sharedPageContentRE = regexp.MustCompile(`\bcontent="([^"]*)"`)
var sharedPageJSONLDRE = regexp.MustCompile(`(?s)<script\b[^>]*\btype="application/ld\+json"[^>]*>(.*?)</script>`)

type storedSharedPage struct {
	Format      int               `json:"format"`
	Binding     string            `json:"binding"`
	HTML        string            `json:"html"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Meta        map[string]string `json:"meta"`
	Document    nextgo.Document   `json:"document"`
	Head        []seo.Element     `json:"head"`
	GeneratedAt time.Time         `json:"generatedAt"`
}
type sharedPageRecord struct {
	id       shared.ID
	policy   shared.Policy
	snapshot storedSharedPage
	sequence int64
}

// The service records real application keys and accepted publications. It never
// supplies a page loader, so observing freshness cannot repair a broken worker.
type sharedPageBackend struct {
	shared.Backend
	t                  *testing.T
	mu                 sync.Mutex
	policies           map[shared.ID]shared.Policy
	records            map[shared.ID]sharedPageRecord
	sequence           int64
	operations         atomic.Int64
	publicationAllowed atomic.Bool
}

func newSharedPageBackend(t *testing.T) *sharedPageBackend {
	t.Helper()
	memory, err := shared.NewMemory(64, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	b := &sharedPageBackend{Backend: memory, t: t, policies: map[shared.ID]shared.Policy{}, records: map[shared.ID]sharedPageRecord{}}
	b.publicationAllowed.Store(true)
	return b
}
func (b *sharedPageBackend) remember(id shared.ID, p shared.Policy) {
	if id.Schema != "demo-pages-v1" {
		return
	}
	b.operations.Add(1)
	p.Tags = append([]string(nil), p.Tags...)
	p.Paths = append([]string(nil), p.Paths...)
	b.mu.Lock()
	b.policies[id] = p
	b.mu.Unlock()
}
func (b *sharedPageBackend) Begin(ctx context.Context, id shared.ID, p shared.Policy, lease time.Duration) (shared.View, error) {
	b.remember(id, p)
	return b.Backend.Begin(ctx, id, p, lease)
}
func (b *sharedPageBackend) BeginStale(ctx context.Context, id shared.ID, p shared.Policy, lease time.Duration) (shared.View, error) {
	b.remember(id, p)
	return b.Backend.BeginStale(ctx, id, p, lease)
}
func (b *sharedPageBackend) Commit(ctx context.Context, id shared.ID, token string, value []byte) (shared.View, error) {
	if id.Schema == "demo-pages-v1" && !b.publicationAllowed.Load() {
		b.t.Error("shared page refresh attempted publication before the Runtime API accepted its response")
	}
	v, err := b.Backend.Commit(ctx, id, token, value)
	if err != nil || !v.Found || id.Schema != "demo-pages-v1" {
		return v, err
	}
	var snapshot storedSharedPage
	if err := json.Unmarshal(v.Value, &snapshot); err != nil {
		b.t.Error(err)
		return v, nil
	}
	if snapshot.Format != 1 || snapshot.Binding != id.Key || snapshot.GeneratedAt.IsZero() || bytes.Contains(v.Value, []byte(`"headHTML"`)) {
		b.t.Error("shared page DTO lacks binding or retains raw head markup")
	}
	b.mu.Lock()
	b.sequence++
	b.records[id] = sharedPageRecord{id: id, policy: b.policies[id], snapshot: snapshot, sequence: b.sequence}
	b.mu.Unlock()
	return v, nil
}
func (b *sharedPageBackend) latest(path string) (sharedPageRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result sharedPageRecord
	found := false
	for _, record := range b.records {
		for _, label := range record.policy.Paths {
			if label == path && (!found || record.sequence > result.sequence) {
				result, found = record, true
			}
		}
	}
	return result, found
}
func (b *sharedPageBackend) commits() int64 { b.mu.Lock(); defer b.mu.Unlock(); return b.sequence }
func (b *sharedPageBackend) peek(ctx context.Context, path string) (shared.View, storedSharedPage, error) {
	record, ok := b.latest(path)
	if !ok {
		return shared.View{}, storedSharedPage{}, fmt.Errorf("no accepted page key for %s", path)
	}
	view, err := b.Backend.BeginStale(ctx, record.id, record.policy, time.Second)
	if err != nil {
		return shared.View{}, storedSharedPage{}, err
	}
	if view.Lease != "" {
		if err := b.Backend.Release(ctx, record.id, view.Lease); err != nil {
			return shared.View{}, storedSharedPage{}, err
		}
	}
	var snapshot storedSharedPage
	if view.Found {
		if err := json.Unmarshal(view.Value, &snapshot); err != nil {
			return shared.View{}, snapshot, err
		}
	}
	return view, snapshot, nil
}
func sharedPageID(body []byte) (string, error) {
	match := sharedPageGenerationRE.FindSubmatch(body)
	if len(match) != 2 {
		return "", fmt.Errorf("missing shared page generation")
	}
	return string(match[1]), nil
}
func sharedPageMeta(body []byte, kind, name string) string {
	for _, tag := range sharedPageMetaRE.FindAll(body, -1) {
		if bytes.Contains(tag, []byte(kind+`="`+name+`"`)) {
			match := sharedPageContentRE.FindSubmatch(tag)
			if len(match) == 2 {
				return html.UnescapeString(string(match[1]))
			}
		}
	}
	return ""
}
func checkSharedPageBody(body []byte, slug string) (string, error) {
	generation, err := sharedPageID(body)
	if err != nil {
		return "", err
	}
	for _, text := range []string{"Shared rendered page: " + slug, `data-nextgo-island="counter"`, "Shared first counter", "Shared second counter", `>3</output>`, `>10</output>`} {
		if !bytes.Contains(body, []byte(text)) {
			return "", fmt.Errorf("shared body missing %q", text)
		}
	}
	if bytes.Contains(body, []byte(sharedSecret)) {
		return "", fmt.Errorf("shared cache credential leaked")
	}
	return generation, nil
}
func checkSharedPageDocument(body []byte, slug string) (string, error) {
	generation, err := checkSharedPageBody(body, slug)
	if err != nil {
		return "", err
	}
	title := "Shared page " + slug + " " + generation
	description := "Public rendered generation " + generation + " shared by Go application instances."
	if !bytes.Contains(body, []byte("<title>"+title+"</title>")) || sharedPageMeta(body, "name", "description") != description || sharedPageMeta(body, "name", "shared-page-generation") != generation || sharedPageMeta(body, "property", "og:title") != title || sharedPageMeta(body, "property", "og:description") != description {
		return "", fmt.Errorf("shared head/body generation mismatch")
	}
	if !bytes.Contains(body, []byte(`lang="en"`)) || !bytes.Contains(body, []byte(`dir="ltr"`)) || !strings.Contains(sharedPageMeta(body, "name", "viewport"), "viewport-fit=cover") || sharedPageMeta(body, "name", "theme-color") != "#115e59" {
		return "", fmt.Errorf("shared document controls missing")
	}
	match := sharedPageJSONLDRE.FindSubmatch(body)
	if len(match) != 2 {
		return "", fmt.Errorf("shared JSON-LD missing")
	}
	var structured map[string]any
	if err := json.Unmarshal(match[1], &structured); err != nil || structured["identifier"] != generation || structured["name"] != title {
		return "", fmt.Errorf("shared structured data mismatch")
	}
	return generation, nil
}
func mustSharedPage(t *testing.T, body []byte, slug string) string {
	t.Helper()
	generation, err := checkSharedPageDocument(body, slug)
	if err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return generation
}
func sharedPageRequest(t *testing.T, client *http.Client, p *process, origin, host, path string, headers http.Header, want int) ([]byte, http.Header) {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		r, err := http.NewRequest("GET", origin+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		r.Header = headers.Clone()
		response, err := client.Do(r)
		if err == nil {
			data := body(t, response)
			if response.StatusCode != want {
				t.Fatalf("shared page %s status %d, want %d: %s", path, response.StatusCode, want, data)
			}
			return data, response.Header
		}
		select {
		case <-p.done:
			t.Fatal(p.logs.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Fatal("shared page startup timed out", p.logs.String())
	return nil, nil
}
func sharedPageAction(t *testing.T, client *http.Client, origin, host, name, slug string, want int) {
	t.Helper()
	r, _ := http.NewRequest("POST", origin+"/_nextgo/actions/"+name, strings.NewReader(url.Values{"slug": {slug}}.Encode()))
	r.Host = host
	r.Header.Set("Origin", "http://"+host)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "text/html")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	data := body(t, response)
	if response.StatusCode != want || want == 303 && response.Header.Get("Location") != "/shared-pages/"+slug {
		t.Fatal(response.StatusCode, response.Header, string(data))
	}
}
func waitSharedPage(t *testing.T, b *sharedPageBackend, path string, accept func(shared.View, storedSharedPage) bool) storedSharedPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		view, snapshot, err := b.peek(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if accept(view, snapshot) {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatal("shared page worker did not settle", view, snapshot.Title)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestSharedPagesHTTPExecutable(t *testing.T) {
	const host = "pages.test"
	recorder := newSharedPageBackend(t)
	srv, down := cacheServiceWithBackend(t, recorder)
	_, store := sharedBackend(t, srv.URL)
	env := sharedEnvironment(srv.URL)
	addrA, addrB := address(t), address(t)
	a, b := start(t, env, "-addr", addrA), start(t, env, "-addr", addrB)
	originA, originB := "http://"+addrA, "http://"+addrB
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	read := func(p *process, origin, path string) (string, http.Header) {
		data, headers := sharedPageRequest(t, client, p, origin, host, path, nil, 200)
		slug := strings.TrimPrefix(strings.Split(path, "?")[0], "/shared-pages/")
		return mustSharedPage(t, data, slug), headers
	}
	first, headers := read(a, originA, "/shared-pages/alpha")
	if headers.Get("X-Nextgo-Cache") != "miss" {
		t.Fatal(headers)
	}
	second, headers := read(b, originB, "/shared-pages/alpha")
	if second != first || headers.Get("X-Nextgo-Cache") != "fresh" || recorder.commits() != 1 {
		t.Fatal("page/head not shared across same-Host processes", first, second, headers, recorder.commits())
	}
	navigationBody, _ := sharedPageRequest(t, client, b, originB, host, "/shared-pages/alpha", http.Header{"X-Nextgo-Navigate": {"1"}}, 200)
	var navigation nextgo.Envelope
	if err := json.Unmarshal(navigationBody, &navigation); err != nil {
		t.Fatal(err)
	}
	stored, ok := recorder.latest("/shared-pages/alpha")
	if !ok {
		t.Fatal("missing stored page")
	}
	wantHead, _ := json.Marshal(stored.snapshot.Head)
	gotHead, _ := json.Marshal(navigation.Head)
	if navigation.HTML != stored.snapshot.HTML || navigation.Title != stored.snapshot.Title || navigation.Description != stored.snapshot.Description || navigation.Meta["shared-page-generation"] != first || navigation.Document != stored.snapshot.Document || !bytes.Equal(gotHead, wantHead) {
		t.Fatal("navigation did not reuse frozen HTML/head/document")
	}
	beta, _ := read(a, originA, "/shared-pages/beta")
	sharedPageAction(t, client, originA, host, "shared-page-path-expire", "alpha", 303)
	alpha, _ := read(b, originB, "/shared-pages/alpha")
	if alpha == first {
		t.Fatal("path expiration retained alpha")
	}
	if got, _ := read(b, originB, "/shared-pages/beta"); got != beta {
		t.Fatal("alpha path expiration discarded beta")
	}
	sharedPageAction(t, client, originA, host, "shared-page-tag-expire", "alpha", 303)
	oldAlpha, _ := read(a, originA, "/shared-pages/alpha")
	oldBeta, _ := read(a, originA, "/shared-pages/beta")
	if oldAlpha == alpha || oldBeta == beta {
		t.Fatal("group tag did not invalidate both slugs")
	}
	beforePrivate := recorder.operations.Load()
	beforeCommits := recorder.commits()
	for _, private := range []http.Header{{"Cookie": {"session=private"}}, {"Cookie": {""}}, {"Authorization": {"Bearer private"}}} {
		data, h := sharedPageRequest(t, client, b, originB, host, "/shared-pages/alpha", private, 200)
		if mustSharedPage(t, data, "alpha") == oldAlpha || h.Get("X-Nextgo-Cache") != "" || !strings.Contains(h.Get("Cache-Control"), "no-store") {
			t.Fatal("credential request reused public snapshot", h)
		}
	}
	if recorder.operations.Load() != beforePrivate || recorder.commits() != beforeCommits {
		t.Fatal("private request touched shared page backend")
	}
	if got, _ := read(b, originB, "/shared-pages/alpha"); got != oldAlpha {
		t.Fatal("private render poisoned public snapshot")
	}
	sharedPageAction(t, client, originA, host, "shared-page-stale", "alpha", 303)
	stale, h := read(b, originB, "/shared-pages/alpha")
	if stale != oldAlpha || h.Get("X-Nextgo-Cache") != "stale" {
		t.Fatal("stale page/head selection lost", stale, h)
	}
	refreshed := waitSharedPage(t, recorder, "/shared-pages/alpha", func(v shared.View, s storedSharedPage) bool {
		generation, _ := sharedPageID([]byte(s.HTML))
		return v.Found && v.FreshFor > 0 && generation != oldAlpha
	})
	newAlpha, _ := sharedPageID([]byte(refreshed.HTML))
	if got, _ := read(a, originA, "/shared-pages/alpha"); got != newAlpha {
		t.Fatal("refreshed body/head unavailable on other replica")
	}
	failAddr := address(t)
	fault := start(t, append(sharedEnvironment(srv.URL), "NEXTGO_DEMO_SHARED_PAGE_FAIL=true"), "-addr", failAddr)
	defer fault.stop(t)
	faultOrigin := "http://" + failAddr
	if got, _ := read(fault, faultOrigin, "/shared-pages/alpha"); got != newAlpha {
		t.Fatal("cache hit executed failing renderer")
	}
	if _, err := store.MarkStale(context.Background(), shared.Selector{Kind: "tag", Value: "shared-pages"}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if got, h := read(fault, faultOrigin, "/shared-pages/alpha"); got != newAlpha || h.Get("X-Nextgo-Cache") != "stale" {
		t.Fatal("transient failure lost last-good snapshot", got, h)
	}
	waitSharedPage(t, recorder, "/shared-pages/alpha", func(v shared.View, s storedSharedPage) bool {
		generation, _ := sharedPageID([]byte(s.HTML))
		return v.Found && !v.Busy && v.FreshFor == 0 && generation == newAlpha
	})
	if _, err := store.MarkStale(context.Background(), shared.Selector{Kind: "tag", Value: "shared-pages"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	failure, _ := sharedPageRequest(t, client, fault, faultOrigin, host, "/shared-pages/alpha", nil, 500)
	if sharedPageGenerationRE.Match(failure) || bytes.Contains(failure, []byte(newAlpha)) {
		t.Fatal("hard expiry served old page/head")
	}
	recovered, _ := read(a, originA, "/shared-pages/alpha")
	if recovered == newAlpha {
		t.Fatal("hard-expiry recovery reused old snapshot")
	}
	down.Store(true)
	failure, _ = sharedPageRequest(t, client, b, originB, host, "/shared-pages/alpha", nil, 500)
	if sharedPageGenerationRE.Match(failure) {
		t.Fatal("service outage silently used process-local page")
	}
	private, _ := sharedPageRequest(t, client, a, originA, host, "/shared-pages/alpha", http.Header{"Cookie": {"session=private"}}, 200)
	if mustSharedPage(t, private, "alpha") == recovered {
		t.Fatal("private outage render used shared snapshot")
	}
	sharedPageAction(t, client, originA, host, "shared-page-tag-expire", "alpha", 500)
	down.Store(false)
	if got, _ := read(b, originB, "/shared-pages/alpha"); got != recovered {
		t.Fatal("failed invalidation falsely acknowledged/discarded remote snapshot")
	}
	beforeRestart := recorder.commits()
	a.stop(t)
	restarted := start(t, env, "-addr", addrA)
	if got, _ := read(restarted, originA, "/shared-pages/alpha"); got != recovered || recorder.commits() != beforeRestart {
		t.Fatal("same-build app restart regenerated shared page")
	}
	variant, _ := read(b, originB, "/shared-pages/alpha?variant=other")
	if variant == recovered {
		t.Fatal("query variants shared page key")
	}
	otherHostBody, _ := sharedPageRequest(t, client, b, originB, "other.pages.test", "/shared-pages/alpha", nil, 200)
	if mustSharedPage(t, otherHostBody, "alpha") == recovered {
		t.Fatal("public Hosts shared page key")
	}
	configAddr := address(t)
	changed := start(t, append(sharedEnvironment(srv.URL), "NEXTGO_DEMO_ASSET_PREFIX=/shared-pages-v2"), "-addr", configAddr)
	defer changed.stop(t)
	configBody, _ := sharedPageRequest(t, client, changed, "http://"+configAddr, host, "/shared-pages/alpha", nil, 200)
	if mustSharedPage(t, configBody, "alpha") == recovered {
		t.Fatal("runtime configuration reused old snapshot")
	}
	restarted.stop(t)
	b.stop(t)
}

type sharedPageEvent struct {
	method, path, payload string
	headers               map[string]string
	status                int
	cache                 string
}

func runSharedPageEvents(t *testing.T, mode, endpoint string, recorder *sharedPageBackend, events []sharedPageEvent, fail bool, before func(context.Context, int) error, check func(context.Context, int, httpEnvelope, []byte) error) {
	t.Helper()
	var issued, reports atomic.Int32
	done := make(chan struct{})
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			index := int(issued.Add(1)) - 1
			if index >= len(events) {
				if index == len(events) {
					close(done)
				}
				<-r.Context().Done()
				return
			}
			if before != nil {
				if err := before(r.Context(), index); err != nil {
					t.Error(err)
				}
			}
			event := events[index]
			headers := map[string]string{"host": "pages.test"}
			for key, value := range event.headers {
				headers[key] = value
			}
			if event.method == "POST" {
				headers["origin"] = "https://pages.test"
				headers["content-type"] = "application/x-www-form-urlencoded"
				headers["accept"] = "text/html"
			}
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": event.path, "headers": headers, "body": event.payload, "requestContext": map[string]any{"domainName": "pages.test", "http": map[string]string{"method": event.method, "path": event.path}}})
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
		if err != nil {
			t.Error(err)
		}
		var envelope httpEnvelope
		var payload []byte
		if mode == "buffered" {
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Error(err)
			}
			payload = []byte(envelope.Body)
			if envelope.Base64 {
				payload, err = base64.StdEncoding.DecodeString(envelope.Body)
				if err != nil {
					t.Error(err)
				}
			}
		} else {
			split := bytes.Index(data, make([]byte, 8))
			if split < 0 {
				t.Error("missing shared-page Lambda stream delimiter")
				w.WriteHeader(500)
				return
			}
			if err := json.Unmarshal(data[:split], &envelope); err != nil {
				t.Error(err)
			}
			payload = data[split+8:]
		}
		index := int(reports.Add(1)) - 1
		if index >= len(events) {
			t.Error("extra shared-page Lambda response")
			w.WriteHeader(500)
			return
		}
		event := events[index]
		if envelope.StatusCode != event.status || sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Cache") != event.cache {
			t.Errorf("shared-page event %d status/cache %d/%q, want %d/%q", index, envelope.StatusCode, sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Cache"), event.status, event.cache)
		}
		if bytes.Contains(payload, []byte(sharedSecret)) {
			t.Error("shared-page Lambda response leaked credential")
		}
		if event.status == 303 && sharedEnvelopeHeader(envelope.Headers, "Location") != "/shared-pages/alpha" {
			t.Error("shared-page action redirect", envelope.Headers)
		}
		if event.status == 500 && (sharedPageGenerationRE.Match(payload) || !bytes.Contains(payload, []byte("Something went wrong"))) {
			t.Error("shared-page failure exposed stale output")
		}
		if check != nil {
			if err := check(r.Context(), index, envelope, payload); err != nil {
				t.Error(err)
			}
		}
		// The observer remains armed until all pre-publication assertions finish.
		recorder.publicationAllowed.Store(true)
		w.WriteHeader(202)
	}))
	defer runtime.Close()
	env := append(sharedEnvironment(endpoint), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
	if fail {
		env = append(env, "NEXTGO_DEMO_SHARED_PAGE_FAIL=true")
	}
	p := start(t, env)
	select {
	case <-done:
	case <-p.done:
		t.Fatal(p.logs.String())
	case <-time.After(20 * time.Second):
		t.Fatal("shared-page Lambda flow timed out", p.logs.String())
	}
	p.stop(t)
	if reports.Load() != int32(len(events)) {
		t.Fatal("missing shared-page Lambda reports", reports.Load(), len(events))
	}
}

func TestSharedPagesLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			t.Run("snapshot-and-publication", func(t *testing.T) {
				recorder := newSharedPageBackend(t)
				srv, _ := cacheServiceWithBackend(t, recorder)
				get := func(slug, cache string) sharedPageEvent {
					return sharedPageEvent{method: "GET", path: "/shared-pages/" + slug, status: 200, cache: cache}
				}
				post := func(action string) sharedPageEvent {
					return sharedPageEvent{method: "POST", path: "/_nextgo/actions/" + action, payload: "slug=alpha", status: 303}
				}
				events := []sharedPageEvent{
					get("alpha", "miss"), get("alpha", "fresh"), get("beta", "miss"), post("shared-page-path-expire"), get("beta", "fresh"), get("alpha", "miss"),
					post("shared-page-tag-expire"), get("alpha", "miss"), get("beta", "miss"), post("shared-page-stale"), get("alpha", "stale"), get("alpha", "fresh"), get("beta", "stale"), get("beta", "fresh"),
				}
				var generations, nextGeneration sync.Map
				previous := func(index int) string { value, _ := generations.Load(index); s, _ := value.(string); return s }
				before := func(ctx context.Context, index int) error {
					if index == 10 || index == 12 {
						recorder.publicationAllowed.Store(false)
					}
					if index == 11 || index == 13 {
						path, oldIndex := "/shared-pages/alpha", 7
						if index == 13 {
							path, oldIndex = "/shared-pages/beta", 8
						}
						view, snapshot, err := recorder.peek(ctx, path)
						if err != nil {
							return err
						}
						generation, err := sharedPageID([]byte(snapshot.HTML))
						if err != nil || !view.Found || view.Busy || view.Lease != "" || view.FreshFor <= 0 || generation == previous(oldIndex) {
							return fmt.Errorf("shared-page refresh not drained before /next %d: %+v generation %q error %v", index, view, generation, err)
						}
						nextGeneration.Store(index, generation)
					}
					return nil
				}
				check := func(ctx context.Context, index int, _ httpEnvelope, payload []byte) error {
					if events[index].status != 200 {
						return nil
					}
					slug := strings.TrimPrefix(events[index].path, "/shared-pages/")
					generation, err := checkSharedPageDocument(payload, slug)
					if err != nil {
						return fmt.Errorf("shared-page event %d: %w", index, err)
					}
					generations.Store(index, generation)
					if oldIndex, ok := map[int]int{1: 0, 4: 2, 10: 7, 12: 8}[index]; ok && generation != previous(oldIndex) {
						return fmt.Errorf("shared-page event %d did not preserve generation %d", index, oldIndex)
					}
					if oldIndex, ok := map[int]int{5: 0, 7: 5, 8: 2, 11: 7, 13: 8}[index]; ok && generation == previous(oldIndex) {
						return fmt.Errorf("shared-page event %d retained expired generation %d", index, oldIndex)
					}
					if index == 11 || index == 13 {
						expected, _ := nextGeneration.Load(index)
						if generation != expected {
							return fmt.Errorf("shared-page next event %d did not use the observed completed refresh", index)
						}
					}
					if index == 10 || index == 12 {
						view, snapshot, err := recorder.peek(ctx, events[index].path)
						if err != nil {
							return err
						}
						storedGeneration, err := sharedPageID([]byte(snapshot.HTML))
						if err != nil || !view.Found || !view.Busy || view.Lease != "" || storedGeneration != generation {
							return fmt.Errorf("shared-page refresh ran before response acceptance: %+v %q %v", view, storedGeneration, err)
						}
					}
					return nil
				}
				runSharedPageEvents(t, mode, srv.URL, recorder, events, false, before, check)
			})
			t.Run("failure-hard-expiry-and-recovery", func(t *testing.T) {
				recorder := newSharedPageBackend(t)
				srv, _ := cacheServiceWithBackend(t, recorder)
				_, store := sharedBackend(t, srv.URL)
				addr := address(t)
				seed := start(t, sharedEnvironment(srv.URL), "-addr", addr)
				defer seed.stop(t)
				client := &http.Client{Timeout: 5 * time.Second}
				defer client.CloseIdleConnections()
				firstBody, _ := sharedPageRequest(t, client, seed, "http://"+addr, "pages.test", "/shared-pages/alpha", nil, 200)
				first := mustSharedPage(t, firstBody, "alpha")
				events := []sharedPageEvent{
					{method: "GET", path: "/shared-pages/alpha", status: 200, cache: "fresh"},
					{method: "GET", path: "/shared-pages/alpha", status: 200, cache: "stale"},
					{method: "GET", path: "/shared-pages/alpha", headers: map[string]string{"cookie": "session=private"}, status: 500},
					{method: "GET", path: "/shared-pages/alpha", status: 500},
				}
				before := func(ctx context.Context, index int) error {
					switch index {
					case 1:
						recorder.publicationAllowed.Store(false)
						_, err := store.MarkStale(ctx, shared.Selector{Kind: "tag", Value: "shared-pages"}, 30*time.Second)
						return err
					case 2:
						view, snapshot, err := recorder.peek(ctx, "/shared-pages/alpha")
						if err != nil {
							return err
						}
						generation, err := sharedPageID([]byte(snapshot.HTML))
						if err != nil || !view.Found || view.Busy || view.FreshFor != 0 || generation != first {
							return fmt.Errorf("failed page refresh did not retain/release complete snapshot: %+v %q %v", view, generation, err)
						}
					case 3:
						if _, err := store.MarkStale(ctx, shared.Selector{Kind: "tag", Value: "shared-pages"}, time.Millisecond); err != nil {
							return err
						}
						time.Sleep(5 * time.Millisecond)
					}
					return nil
				}
				check := func(ctx context.Context, index int, _ httpEnvelope, payload []byte) error {
					if index > 1 {
						return nil
					}
					generation, err := checkSharedPageDocument(payload, "alpha")
					if err != nil || generation != first {
						return fmt.Errorf("faulting worker changed cached body/head: %q %v", generation, err)
					}
					if index == 1 {
						view, snapshot, err := recorder.peek(ctx, "/shared-pages/alpha")
						if err != nil {
							return err
						}
						storedGeneration, err := sharedPageID([]byte(snapshot.HTML))
						if err != nil || !view.Busy || !view.Found || storedGeneration != first {
							return fmt.Errorf("failed refresh ran before response acceptance: %+v %q %v", view, storedGeneration, err)
						}
					}
					return nil
				}
				runSharedPageEvents(t, mode, srv.URL, recorder, events, true, before, check)
				view, _, err := recorder.peek(context.Background(), "/shared-pages/alpha")
				if err != nil || view.Found {
					t.Fatal("hard-expired failure retained public page", view, err)
				}
				recoveryBody, h := sharedPageRequest(t, client, seed, "http://"+addr, "pages.test", "/shared-pages/alpha", nil, 200)
				recovered := mustSharedPage(t, recoveryBody, "alpha")
				if recovered == first || h.Get("X-Nextgo-Cache") != "miss" {
					t.Fatal("healthy renderer did not recover after failing worker", h, recovered)
				}
				runSharedPageEvents(t, mode, srv.URL, recorder, []sharedPageEvent{{method: "GET", path: "/shared-pages/alpha", status: 200, cache: "fresh"}}, false, nil, func(_ context.Context, _ int, _ httpEnvelope, payload []byte) error {
					generation, err := checkSharedPageDocument(payload, "alpha")
					if err != nil || generation != recovered {
						return fmt.Errorf("new worker did not read recovered snapshot %q: %v", generation, err)
					}
					return nil
				})
			})
		})
	}
}
