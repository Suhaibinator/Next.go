package nextgo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"maps"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

var (
	ErrDeferredMetadataResponse = errors.New("nextgo: deferred metadata cannot mutate response controls")
	ErrDeferredMetadataDocument = errors.New("nextgo: deferred metadata cannot change document attributes or viewport")
	ErrDeferredMetadataPartial  = errors.New("nextgo: deferred metadata is unsupported in partial public shells")
	ErrAfterDeferredMetadata    = errors.New("nextgo: After is unavailable in deferred metadata")
	ErrDeferredMetadataFailed   = errors.New("nextgo: deferred metadata callback failed")
)

type MetadataStreamingOptions struct {
	HTML            bool
	HTMLLimitedBots []string
}
type deferredMetadataReadonlyKey struct{}

var metadataBrowserFamilies = []string{"chrome/", "chromium/", "firefox/", "safari/", "edg/", "opr/", "fxios/", "crios/"}
var metadataBotFamilies = []string{"bot", "crawler", "spider", "slurp", "facebookexternalhit", "whatsapp", "telegram", "discord"}

func normalizeMetadataStreaming(options *MetadataStreamingOptions) error {
	if len(options.HTMLLimitedBots) > 64 {
		return fmt.Errorf("metadata: at most 64 literal bot blockers")
	}
	values := make([]string, 0, len(options.HTMLLimitedBots))
	for _, value := range options.HTMLLimitedBots {
		if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("metadata: invalid literal bot blocker")
		}
		values = append(values, strings.ToLower(strings.Clone(value)))
	}
	sort.Strings(values)
	options.HTMLLimitedBots = values[:0]
	for _, value := range values {
		if len(options.HTMLLimitedBots) == 0 || options.HTMLLimitedBots[len(options.HTMLLimitedBots)-1] != value {
			options.HTMLLimitedBots = append(options.HTMLLimitedBots, value)
		}
	}
	return nil
}
func metadataConfigurationPolicy(previous string, options MetadataStreamingOptions) string {
	data, _ := json.Marshal([]any{options, metadataBrowserFamilies, metadataBotFamilies})
	sum := sha256.Sum256(append([]byte("nextgo.metadata.streaming.v1\x00"+previous+"\x00"), data...))
	return hex.EncodeToString(sum[:])
}
func (a *App) metadataBrowser(r *http.Request) bool {
	ua := r.UserAgent()
	if ua == "" || len(ua) > 2048 {
		return false
	}
	ua = strings.ToLower(ua)
	for _, v := range metadataBotFamilies {
		if strings.Contains(ua, v) {
			return false
		}
	}
	for _, v := range a.config.Streaming.Metadata.HTMLLimitedBots {
		if strings.Contains(ua, v) {
			return false
		}
	}
	if !strings.HasPrefix(ua, "mozilla/5.0") {
		return false
	}
	for _, v := range metadataBrowserFamilies {
		if strings.Contains(ua, v) {
			return true
		}
	}
	return false
}
func metadataNegotiated(r *http.Request) bool {
	v := r.Header.Values(navigation.MetadataHeader)
	return len(v) == 1 && v[0] == "1"
}

type metadataResult struct {
	page    Page
	head    []seo.Element
	markup  string
	indices []int
	err     error
}
type deferredMetadataPlan struct {
	app                            *App
	callback                       MetadataFunc
	fallback                       Page
	context                        *Context
	frozen                         frozenHeadResources
	document                       Document
	viewport                       *seo.Viewport
	legacyTitle, legacyDescription string
}

// Check borrowed application values before cloning maps, constructing nodes or
// escaping text. Callback allocations themselves remain application-owned.
func preflightMetadata(m seo.Metadata, title, description string, legacy map[string]string) error {
	if len(m.Icons)+len(m.Alternates)+len(m.OpenGraph.Images)+len(m.Twitter.Images) > navigation.MaxMetadataElements || len(legacy) > navigation.MaxMetadataElements {
		return fmt.Errorf("metadata raw element limit exceeded")
	}
	used := 0
	add := func(s string) bool {
		if len(s) > navigation.MaxMetadataBytes-used {
			return false
		}
		used += len(s)
		return true
	}
	for _, s := range []string{title, description, m.BaseURL, m.TitleTemplate, m.Title, m.Description, m.Language, m.Direction, m.Canonical, m.Robots, m.OpenGraph.Title, m.OpenGraph.Description, m.OpenGraph.URL, m.OpenGraph.Type, m.OpenGraph.SiteName, m.OpenGraph.Locale, m.Twitter.Card, m.Twitter.Title, m.Twitter.Description, m.Twitter.Site, m.Twitter.Creator} {
		if !add(s) {
			return fmt.Errorf("metadata raw byte limit exceeded")
		}
	}
	if m.Manifest != nil && !add(*m.Manifest) {
		return fmt.Errorf("metadata raw byte limit exceeded")
	}
	for k, v := range legacy {
		if !add(k) || !add(v) {
			return fmt.Errorf("metadata raw byte limit exceeded")
		}
	}
	for k, v := range m.Alternates {
		if !add(k) || !add(v) {
			return fmt.Errorf("metadata raw byte limit exceeded")
		}
	}
	for _, image := range m.OpenGraph.Images {
		if !add(image.URL) || !add(image.Alt) {
			return fmt.Errorf("metadata raw byte limit exceeded")
		}
	}
	for _, image := range m.Twitter.Images {
		if !add(image) {
			return fmt.Errorf("metadata raw byte limit exceeded")
		}
	}
	for _, icon := range m.Icons {
		for _, s := range []string{icon.URL, icon.Rel, icon.Type, icon.Sizes} {
			if !add(s) {
				return fmt.Errorf("metadata raw byte limit exceeded")
			}
		}
	}
	if v := m.Viewport; v != nil {
		if len(v.ThemeColors) > 16 {
			return fmt.Errorf("metadata viewport limit exceeded")
		}
		for _, s := range []string{v.Width, v.Height, v.Fit, v.InteractiveWidget, v.ColorScheme} {
			if !add(s) {
				return fmt.Errorf("metadata raw byte limit exceeded")
			}
		}
		for _, color := range v.ThemeColors {
			if !add(color.Color) || !add(color.Media) {
				return fmt.Errorf("metadata raw byte limit exceeded")
			}
		}
	}
	return nil
}
func (a *App) prepareDeferredMetadata(c *Context, page Page) (*deferredMetadataPlan, error) {
	if c == nil || c.Request == nil || page.DeferredMetadata == nil {
		return nil, fmt.Errorf("metadata: request and callback required")
	}
	if err := preflightMetadata(page.Metadata, page.Title, page.Description, page.Meta); err != nil {
		return nil, err
	}
	page, err := normalizePageMetadata(page)
	if err != nil {
		return nil, err
	}
	page.Metadata = seo.Merge(seo.Metadata{}, page.Metadata)
	page.Meta = ownStringMap(page.Meta)
	page.preparedHead = nil
	page = a.withManagedMetadataHead(page)
	document, err := pageDocument(page)
	if err != nil {
		return nil, err
	}
	frozen, err := freezeHeadResources(page)
	if err != nil {
		return nil, err
	}
	frozen.boundedMetadata = true
	if _, _, _, err = assembleHead(page, a.offlineRenderPaths(c.Context), frozen, ""); err != nil {
		return nil, err
	}
	view := &Context{Context: c.Context, Request: c.Request.Clone(c.Context), Params: maps.Clone(c.Params), Query: cloneQuery(c.Query), responseRef: c.responseControls()}
	p := &deferredMetadataPlan{app: a, callback: page.DeferredMetadata, fallback: page, context: view, frozen: frozen, document: document, viewport: page.Metadata.Viewport, legacyTitle: page.Title, legacyDescription: page.Description}
	p.fallback.Resources = nil
	p.fallback.JSONLD = nil
	p.fallback.DeferredMetadata = nil
	p.fallback.deferredContext = nil
	return p, nil
}
func (p *deferredMetadataPlan) build(page Page, owner string) (metadataResult, error) {
	page.preparedHead = nil
	page = p.app.withManagedMetadataHead(page)
	page.Title = p.legacyTitle
	page.Description = p.legacyDescription
	if page.Title == "" {
		page.Title = page.Metadata.Title
	}
	if page.Description == "" {
		page.Description = page.Metadata.Description
	}
	head, markup, indices, err := assembleHead(page, p.app.offlineRenderPaths(p.context.Context), p.frozen, owner)
	if err != nil {
		return metadataResult{}, err
	}
	page.Resources = nil
	page.JSONLD = nil
	page.DeferredMetadata = nil
	page.deferredContext = nil
	page.deferredPlan = nil
	return metadataResult{page: page, head: head, markup: markup, indices: indices}, nil
}
func viewportEqual(a, b *seo.Viewport) bool {
	var x, y seo.Viewport
	if a != nil {
		x = *a
	}
	if b != nil {
		y = *b
	}
	if len(x.ThemeColors) == 0 {
		x.ThemeColors = nil
	}
	if len(y.ThemeColors) == 0 {
		y.ThemeColors = nil
	}
	// Resolved Document compares width/scale/zoom defaults. Preserve all other
	// effective viewport state, including color scheme and theme colors.
	x.Width = ""
	x.Height = ""
	x.InitialScale = 0
	x.MinimumScale = 0
	x.MaximumScale = 0
	x.UserScalable = nil
	x.Fit = ""
	x.InteractiveWidget = ""
	y.Width = ""
	y.Height = ""
	y.InitialScale = 0
	y.MinimumScale = 0
	y.MaximumScale = 0
	y.UserScalable = nil
	y.Fit = ""
	y.InteractiveWidget = ""
	return reflect.DeepEqual(x, y)
}
func (p *deferredMetadataPlan) resolve(ctx context.Context, drain func()) (result metadataResult) {
	var readonly *Context
	defer func() {
		// Panic and Goexit must retain the same physical ownership as an ordinary
		// return. The drain is idempotent and ignores waiter cancellation.
		drain()
		if v := recover(); v != nil {
			result = metadataResult{err: fmt.Errorf("deferred metadata callback panicked")}
		}
		if readonly != nil {
			if controlErr := readonly.response.seal().err; controlErr != nil {
				p.context.responseControls().recordDeferredViolation()
				result = metadataResult{err: controlErr}
			}
		}
	}()
	ctx = postresponse.BlockRegistration(ctx, ErrAfterDeferredMetadata)
	readonly = &Context{Params: maps.Clone(p.context.Params), Query: cloneQuery(p.context.Query), response: responseState{readonly: ErrDeferredMetadataResponse}}
	ctx = context.WithValue(ctx, deferredMetadataReadonlyKey{}, readonly.responseControls())
	readonly.Context = ctx
	readonly.Request = p.context.Request.Clone(ctx)
	// A live request body is mutable transport ownership, not metadata input.
	readonly.Request.Body = http.NoBody
	readonly.Request.GetBody = nil
	contribution, err := p.callback(readonly, seo.Merge(seo.Metadata{}, p.fallback.Metadata))
	// A returned callback can still own framework Memo work. Wait for actual
	// completion before reading its contribution or making a success observable;
	// retained work may still attempt a forbidden response/After mutation.
	drain()
	if controlErr := readonly.response.seal().err; controlErr != nil {
		p.context.responseControls().recordDeferredViolation()
		return metadataResult{err: controlErr}
	}
	if err != nil {
		return metadataResult{err: ErrDeferredMetadataFailed}
	}
	if err = ctx.Err(); err != nil {
		return metadataResult{err: err}
	}
	if err = preflightMetadata(contribution, "", "", nil); err != nil {
		return metadataResult{err: err}
	}
	// A returned unchanged defensive parent is still an inherited, already
	// formatted title. Only a new title receives the inherited template once.
	if contribution.Title == p.fallback.Metadata.Title {
		contribution.Title = ""
	}
	metadata, err := seo.ResolveContribution(p.fallback.Metadata, contribution)
	if err != nil {
		return metadataResult{err: err}
	}
	document, err := seo.ResolveDocument(metadata)
	if err != nil {
		return metadataResult{err: err}
	}
	if err = preflightMetadata(metadata, p.legacyTitle, p.legacyDescription, p.fallback.Meta); err != nil {
		return metadataResult{err: err}
	}
	if document != p.document || !viewportEqual(p.viewport, metadata.Viewport) {
		return metadataResult{err: ErrDeferredMetadataDocument}
	}
	page := p.fallback
	page.Metadata = metadata
	documentCopy := p.document
	page.document = &documentCopy
	result, err = p.build(page, "")
	if err != nil {
		return metadataResult{err: err}
	}
	if _, err = metadataTerminal(result, "00000000000000000000000000000000", false); err != nil {
		return metadataResult{err: err}
	}
	return result
}

type metadataJob struct{ result <-chan metadataResult }

type metadataBodyEvent struct {
	event ui.StreamEvent
	done  bool
	err   error
}

// This loop is the only HTTP emitter. The UI worker owns a bounded event slot;
// the metadata worker owns a bounded result slot. Neither writes a response.
func renderMetadataEvents(ctx context.Context, page Page, emitBody func(ui.StreamEvent) error, emitMetadata func(metadataResult) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan metadataBodyEvent, 1)
	go func() {
		err := ui.RenderEvents(ctx, page.Body, func(event ui.StreamEvent) error {
			select {
			case events <- metadataBodyEvent{event: event}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		select {
		case events <- metadataBodyEvent{done: true, err: err}:
		case <-ctx.Done():
		}
	}()
	metadata := page.deferredJob.result
	bodyDone, metadataDone, shell := false, false, false
	var pending *metadataResult
	terminal := func(result metadataResult) error {
		if err := ctx.Err(); err != nil {
			result = metadataResult{err: err}
		}
		if err := emitMetadata(result); err != nil {
			return err
		}
		metadataDone = true
		return nil
	}
	for !bodyDone || !metadataDone {
		select {
		case item := <-events:
			if item.done {
				bodyDone = true
				events = nil
				if item.err != nil {
					if shell && !metadataDone {
						_ = terminal(metadataResult{err: item.err})
					}
					return item.err
				}
				continue
			}
			if !shell {
				select {
				case result := <-metadata:
					metadata = nil
					if result.err != nil {
						return result.err
					}
					pending = &result
				default:
				}
			}
			if err := emitBody(item.event); err != nil {
				return err
			}
			if item.event.ID == "" {
				shell = true
				if pending != nil {
					if err := terminal(*pending); err != nil {
						return err
					}
					pending = nil
				}
			}
		case result := <-metadata:
			metadata = nil
			if !shell {
				if result.err != nil {
					return result.err
				}
				pending = &result
			} else if err := terminal(result); err != nil {
				return err
			}
		case <-ctx.Done():
			if shell && !metadataDone {
				_ = terminal(metadataResult{err: ctx.Err()})
			}
			return ctx.Err()
		}
	}
	return ctx.Err()
}
func startMetadataJob(ctx context.Context, plan *deferredMetadataPlan) *metadataJob {
	results := make(chan metadataResult, 1)
	var once sync.Once
	deliver := func(r metadataResult) {
		once.Do(func() {
			select {
			case results <- r:
			case <-ctx.Done():
			}
		})
	}
	go func() {
		err := ui.RunStreamLoader(ctx, func(work context.Context) error {
			lease, e := beginMetadataMemoLease(work)
			if e != nil {
				return e
			}
			var drained sync.Once
			drain := func() { drained.Do(func() { lease.FinishCallback(); lease.Wait() }) }
			defer drain()
			result := plan.resolve(work, drain)
			deliver(result)
			return result.err
		})
		if err != nil {
			deliver(metadataResult{err: err})
		}
	}()
	return &metadataJob{result: results}
}
func (a *App) beginMetadata(ctx context.Context, plan *deferredMetadataPlan) (context.Context, context.CancelFunc, func(), *metadataJob, error) {
	ctx = metadataValueContext{Context: ctx, values: plan.context.Context}
	ctx, cancel := context.WithTimeout(ctx, a.config.Streaming.Timeout)
	work, release, err := a.config.streamLimiter.Begin(ctx)
	if err != nil {
		cancel()
		return nil, nil, nil, nil, err
	}
	return work, cancel, release, startMetadataJob(work, plan), nil
}

type metadataValueContext struct {
	context.Context
	values context.Context
}

func (c metadataValueContext) Value(key any) any { return c.values.Value(key) }
func waitMetadata(ctx context.Context, job *metadataJob) metadataResult {
	if err := ctx.Err(); err != nil {
		return metadataResult{err: err}
	}
	select {
	case r := <-job.result:
		if err := ctx.Err(); err != nil {
			return metadataResult{err: err}
		}
		return r
	case <-ctx.Done():
		return metadataResult{err: ctx.Err()}
	}
}
func metadataTerminal(result metadataResult, owner string, failed bool) ([]byte, error) {
	payload := navigation.MetadataPayload{Version: navigation.MetadataVersion, Owner: owner}
	if !failed {
		payload.Title = result.page.Title
		payload.Description = result.page.Description
		payload.Elements = make([]navigation.MetadataElement, 0, len(result.indices))
		for _, i := range result.indices {
			e := result.head[i]
			payload.Elements = append(payload.Elements, navigation.MetadataElement{Tag: e.Tag, Attrs: e.Attrs, Text: e.Text})
		}
	}
	data, err := navigation.EncodeMetadataPayload(payload, failed)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(navigation.StreamFrame{Version: navigation.StreamVersion, Type: "metadata", Failed: failed, Metadata: data})
	if err != nil {
		return nil, err
	}
	if len(encoded) > navigation.MaxMetadataBytes {
		return nil, fmt.Errorf("metadata terminal exceeds byte limit")
	}
	return encoded, nil
}
func metadataOwner() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}
func (a *App) deferredAllowed(r *http.Request, page Page) bool {
	if !page.Stream || r.Method != http.MethodGet || a.config.Phase != PhaseProduction || a.config.PrerenderBuildMode || offlineCaptureFor(r) != nil || !a.metadataBrowser(r) {
		return false
	}
	if r.Header.Get("X-Nextgo-Navigate") == "1" {
		return metadataNegotiated(r) && r.Header.Get(navigation.StreamHeader) == fmt.Sprint(navigation.StreamVersion) && r.Header.Get(navigation.PurposeHeader) == ""
	}
	return a.config.Streaming.Metadata.HTML
}
func (a *App) resolveSnapshotMetadata(c *Context, page Page) (Page, error) {
	if page.DeferredMetadata == nil {
		return page, nil
	}
	plan, err := a.prepareDeferredMetadata(c, page)
	if err != nil {
		return Page{}, err
	}
	work, cancel, release, job, err := a.beginMetadata(c.Context, plan)
	if err != nil {
		return Page{}, err
	}
	defer cancel()
	defer release()
	result := waitMetadata(work, job)
	if c.responseControls().hasDeferredViolation() {
		return Page{}, ErrPublicCacheResponse
	}
	if result.err != nil {
		return Page{}, result.err
	}
	result.page.preparedHead = &renderedHead{elements: result.head, markup: result.markup}
	return result.page, nil
}
