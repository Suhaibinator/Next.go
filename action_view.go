package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/internal/actionview"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/publicorigin"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

type actionViewRequestKey struct{}

// serveActionView negotiates before invoking an action. The source is a public
// path/query, not an authority or an authorization decision. Native middleware
// and action/page authorization still run with the original credentials.
func (a *App) serveActionView(w http.ResponseWriter, r *http.Request) {
	fail := func(status int) {
		w.Header().Set(actionwire.ProtocolHeader, actionwire.Protocol)
		if revision := r.Header.Get(actionwire.RevisionHeader); len(revision) <= 64 && actionwire.ValidSegment(revision) {
			w.Header().Set(actionwire.RevisionHeader, revision)
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
		a.draftResponse(w, r)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(actions.Response{Error: &actions.Error{Code: "view_unavailable", Message: "Reload before requesting refreshed action UI."}})
	}
	one := func(name, expected string) bool {
		values := r.Header.Values(name)
		return len(values) == 1 && values[0] == expected
	}
	if r.Method != http.MethodPost || !one(actionview.Header, "1") || !one(actionview.BuildHeader, a.config.BuildID) || !one(navigation.ConfigurationHeader, a.configurationID) || len(r.Header.Values(navigation.DraftHeader)) > 1 || r.Header.Get(navigation.DraftHeader) != a.draftState(r) {
		fail(http.StatusConflict)
		return
	}
	router, ok := a.config.Actions.(*actions.Router)
	if !ok || router == nil || len(r.Header.Values(actionview.URLHeader)) != 1 || r.Header.Get("Accept") != actionview.MediaType {
		fail(http.StatusConflict)
		return
	}
	source := r.Header.Get(actionview.URLHeader)
	probe, cancel, err := a.actionViewGET(r, source)
	if err != nil {
		fail(http.StatusBadRequest)
		return
	}
	cancel()
	if err := a.actionViewTarget(probe, 0); err != nil {
		fail(http.StatusBadRequest)
		return
	}
	if values := r.Header.Values(navigation.SlotsHeader); len(values) > 1 {
		fail(http.StatusBadRequest)
		return
	} else if len(values) == 1 {
		var state navigation.SlotState
		if len(values[0]) > navigation.MaxSlotStateBytes || wirejson.Unique([]byte(values[0])) != nil || wirejson.Strict([]byte(values[0]), &state) != nil || state.Validate() != nil || state.Configuration != a.configurationID {
			fail(http.StatusBadRequest)
			return
		}
	}
	local := r.Clone(r.Context())
	local.URL.Path = strings.TrimPrefix(local.URL.Path, "/_nextgo/actions/")
	local.URL.RawPath = ""
	router.ServeWithView(w, local, func(w http.ResponseWriter, req *http.Request, success actions.ViewSuccess) {
		a.renderActionView(w, r, source, success)
	})
}

// NewRequest clears PathValue/Pattern/form/body state that Clone would retain.
// WithRequestURI creates a new Memo while preserving the existing outer After
// scope and platform collector. Calling App.ServeHTTP here would finish a nested
// scope before the combined response was accepted by the platform.
func (a *App) actionViewGET(original *http.Request, source string) (*http.Request, context.CancelFunc, error) {
	if len(source) == 0 || len(source) > actionview.MaxURL || strings.Contains(source, "#") || !localURL(source) {
		return nil, nil, fmt.Errorf("invalid action view source")
	}
	u, err := url.ParseRequestURI(source)
	if err != nil || u.Fragment != "" || u.Host != "" || u.Scheme != "" {
		return nil, nil, fmt.Errorf("invalid action view URL")
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return nil, nil, fmt.Errorf("invalid action view query")
	}
	get, err := http.NewRequestWithContext(cache.WithBypass(context.WithValue(original.Context(), actionViewRequestKey{}, true)), http.MethodGet, source, nil)
	if err != nil {
		return nil, nil, err
	}
	get.Host, get.RemoteAddr, get.TLS = original.Host, original.RemoteAddr, original.TLS
	get.Proto, get.ProtoMajor, get.ProtoMinor = original.Proto, original.ProtoMajor, original.ProtoMinor
	get.Header = original.Header.Clone()
	for key := range get.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "content-") || strings.HasPrefix(lower, "x-nextgo-action-") || lower == "accept-encoding" || lower == "range" || lower == "if-none-match" || lower == "if-modified-since" || lower == "expect" || lower == strings.ToLower(navigation.ShellHeader) || lower == strings.ToLower(navigation.ShellProtocolHeader) || lower == strings.ToLower(navigation.PurposeHeader) {
			delete(get.Header, key)
		}
	}
	get.Body = http.NoBody
	get.RequestURI = source
	get.Header.Set("Accept", "application/json")
	get.Header.Set("X-Nextgo-Navigate", "1")
	get.Header.Set(navigation.StreamHeader, "1")
	get, cancel := withRequestURI(get)
	local, inside := localRequest(get, a.paths.BasePath)
	if !inside {
		cancel()
		return nil, nil, fmt.Errorf("action view leaves application scope")
	}
	return a.logicalRequest(local), cancel, nil
}

// This check runs before the mutation and again after GET middleware, before
// routing can invoke an API/configured handler/proxy. Arbitrary middleware can
// change its decision after a commit; that produces a confirmed view failure,
// never an automatic second mutation.
func (a *App) actionViewTarget(r *http.Request, depth int) error {
	if depth >= 8 || assetRequest(r) || strings.HasPrefix(r.URL.Path, "/_nextgo/") {
		return fmt.Errorf("ineligible action view target")
	}
	for path := range a.config.Handlers {
		if r.URL.Path == path || strings.HasSuffix(path, "/") && strings.HasPrefix(r.URL.Path, path) {
			return fmt.Errorf("configured handler is not an action view")
		}
	}
	for _, rule := range a.redirects {
		if params, ok := rule.match(r); ok {
			target, err := rule.destination(params, r.URL)
			if err != nil || rule.external() {
				return fmt.Errorf("ineligible action view redirect")
			}
			copy := r.Clone(r.Context())
			copy.URL = target
			return a.actionViewTarget(copy, depth+1)
		}
	}
	copy := r.Clone(r.Context())
	if rule, target, err := a.resolveRewrite(copy); err != nil {
		return err
	} else if rule != nil {
		if rule.external() {
			return fmt.Errorf("proxy is not an action view")
		}
		copy.URL = target
	}
	if assetRequest(copy) || strings.HasPrefix(copy.URL.Path, "/_nextgo/") {
		return fmt.Errorf("reserved target is not an action view")
	}
	for _, route := range a.routes {
		if _, ok := match(route.Pattern, strings.TrimSuffix(copy.URL.EscapedPath(), "/")); ok || copy.URL.Path == "/" && route.Pattern == "/" {
			if route.API != nil {
				return fmt.Errorf("API is not an action view")
			}
			return nil
		}
	}
	// Unknown ordinary URLs render the application's custom 404, just as a
	// direct request does. A not-found document can contain layout actions too.
	return nil
}

func (a *App) renderActionView(w http.ResponseWriter, original *http.Request, source string, success actions.ViewSuccess) {
	reply := &actionViewReply{app: a, output: w, request: original, result: success.Encoded}
	defer func() {
		if cause := recover(); cause != nil {
			a.config.Logger.Printf("action view panic: %v", cause)
			reply.fail(http.StatusInternalServerError)
		}
	}()
	ctx, stop := context.WithTimeout(original.Context(), a.config.Streaming.Timeout)
	defer stop()
	original = original.WithContext(ctx)
	// Keep cookies staged by the action/redirect chain available to subsequent
	// same-host GETs, as a browser following those responses would. This jar is
	// request-local and never carries credentials to another application scope.
	jar, _ := cookiejar.New(nil)
	publicURL := func(uri string) *url.URL {
		u, _ := url.Parse(uri)
		// Cookie paths match the public escaped path. cookiejar compares Path;
		// give it the wire spelling rather than decoding encoded separators.
		u.Path, u.RawPath = u.EscapedPath(), ""
		origin, err := publicorigin.Request(original)
		if err == nil {
			u.Scheme, u.Host = origin.Scheme(), origin.Host()
		}
		return u
	}
	// Incoming Cookie has no path/domain attributes. Preserve its values and
	// order rather than inventing scopes in the jar (which could resurrect a
	// deleted scoped preview cookie). Only known Set-Cookie updates enter it.
	type cookieUpdate struct {
		name, domain, path string
		secure             bool
	}
	var cookieUpdates []cookieUpdate
	applyCookies := func(location *url.URL, headers http.Header) {
		cookies := (&http.Response{Header: headers}).Cookies()
		for _, cookie := range cookies {
			domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
			if domain == "" {
				domain = strings.ToLower(location.Hostname())
			}
			path := cookie.Path
			if !strings.HasPrefix(path, "/") {
				path = actionViewCookiePath(location.Path)
			}
			cookieUpdates = append(cookieUpdates, cookieUpdate{cookie.Name, domain, path, cookie.Secure})
		}
		jar.SetCookies(location, cookies)
	}
	applyCookies(publicURL(requestURI(original)), w.Header())
	if len(success.RevalidateTags)+len(success.RevalidatePaths) > 128 {
		reply.fail(http.StatusInternalServerError)
		return
	}
	for _, group := range []struct {
		kind   string
		values []string
	}{{"tag", success.RevalidateTags}, {"path", success.RevalidatePaths}} {
		for _, value := range group.values {
			if err := a.ExpireCache(ctx, shared.Selector{Kind: group.kind, Value: value}); err != nil {
				a.config.Logger.Printf("action view invalidation: %v", err)
				reply.fail(http.StatusServiceUnavailable)
				return
			}
		}
	}
	if success.Redirect != "" {
		source = success.Redirect
	}
	for depth := 0; depth < 8; depth++ {
		// Fragments belong to browser history, not the fresh GET representation.
		u, err := url.Parse(source)
		if err != nil {
			reply.fail(409)
			return
		}
		u.Fragment, u.RawFragment = "", ""
		get, cancel, err := a.actionViewGET(original, u.String())
		if err != nil {
			reply.fail(409)
			return
		}
		if err := a.actionViewTarget(get, 0); err != nil {
			cancel()
			reply.fail(409)
			return
		}
		get.Header.Del("Cookie")
		location := publicURL(u.String())
		changedCookies := make(map[string]bool)
		for _, update := range cookieUpdates {
			host := strings.ToLower(location.Hostname())
			pathMatches := location.Path == update.path || strings.HasPrefix(location.Path, update.path) && (strings.HasSuffix(update.path, "/") || strings.HasPrefix(strings.TrimPrefix(location.Path, update.path), "/"))
			if (host == update.domain || strings.HasSuffix(host, "."+update.domain)) && pathMatches && (!update.secure || location.Scheme == "https") {
				changedCookies[update.name] = true
			}
		}
		for _, cookie := range original.Cookies() {
			if !changedCookies[cookie.Name] {
				get.AddCookie(cookie)
			}
		}
		for _, cookie := range jar.Cookies(publicURL(u.String())) {
			if !changedCookies[cookie.Name] {
				continue
			}
			get.AddCookie(cookie)
		}
		if a.config.Draft != nil {
			public := get.Clone(get.Context())
			public.URL = u
			verified := a.config.Draft.BindRequest(public)
			if a.draftState(verified) != a.draftState(original) {
				cancel()
				reply.fail(http.StatusConflict)
				return
			}
			get = get.WithContext(verified.Context())
		}
		capture := &actionViewWriter{reply: reply, header: make(http.Header), pagePath: publicURL(u.String()).Path}
		wrapped, request, valid := a.bindHeaderRules(capture, get)
		if valid {
			a.handler.ServeHTTP(wrapped, request)
		}
		cancel()
		cookies, cookieErr := actionViewPageCookies(capture.header.Values("Set-Cookie"), publicURL(u.String()).Path)
		if cookieErr != nil {
			reply.fail(http.StatusInternalServerError)
			return
		}
		capture.header["Set-Cookie"] = cookies
		if capture.status >= 300 && capture.status < 400 && !reply.started {
			for _, cookie := range capture.header.Values("Set-Cookie") {
				reply.output.Header().Add("Set-Cookie", cookie)
			}
			applyCookies(publicURL(u.String()), capture.header)
			source = capture.header.Get("Location")
			continue
		}
		if err := capture.finish(); err != nil {
			a.config.Logger.Printf("action view render: %v", err)
			reply.fail(http.StatusInternalServerError)
		}
		return
	}
	reply.fail(http.StatusLoopDetected)
}

// These headers reach the browser on the action URL. Make a page cookie's
// default path explicit so its browser scope agrees with the page that set it.
func actionViewPageCookies(values []string, path string) ([]string, error) {
	result := append([]string(nil), values...)
	defaultPath := actionViewCookiePath(path)
	for i, value := range result {
		cookies := (&http.Response{Header: http.Header{"Set-Cookie": []string{value}}}).Cookies()
		if len(cookies) == 1 && !strings.HasPrefix(cookies[0].Path, "/") {
			if err := (&http.Cookie{Name: "path-validation", Value: "x", Path: defaultPath}).Valid(); err != nil {
				return nil, fmt.Errorf("page cookie default path cannot be represented on an action response")
			}
			result[i] = value + "; Path=" + defaultPath
		}
	}
	return result, nil
}

func actionViewCookiePath(path string) string {
	if end := strings.LastIndex(path, "/"); strings.HasPrefix(path, "/") && end > 0 {
		return path[:end]
	}
	return "/"
}

type actionViewReply struct {
	app           *App
	output        http.ResponseWriter
	request       *http.Request
	result        []byte
	started, done bool
	used, count   int
}

func (r *actionViewReply) start(headers http.Header) error {
	if r.started {
		return nil
	}
	confirmation, err := json.Marshal(actionview.Frame{Version: actionview.Version, Type: "result", Result: r.result})
	if err == nil {
		_, err = actionview.DecodeFrame(confirmation)
	}
	if err != nil {
		// A malformed success value cannot be a confirmation. The mutation may
		// have committed; keep this distinct from a public validation failure.
		r.done = true
		r.output.Header().Set("Cache-Control", "private, no-store")
		r.output.Header().Set("Content-Type", "application/json; charset=utf-8")
		r.output.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(r.output).Encode(actions.Response{Error: &actions.Error{Code: "internal_error", Message: "The action outcome could not be confirmed. Reconcile before retrying."}})
		return err
	}
	for key, values := range headers {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-nextgo-") || lower == "content-type" || lower == "content-length" || lower == "content-encoding" || lower == "location" || lower == "cache-control" {
			continue
		}
		if lower == "set-cookie" || lower == "vary" {
			for _, value := range values {
				r.output.Header().Add(key, value)
			}
		} else {
			r.output.Header()[key] = append([]string(nil), values...)
		}
	}
	header := r.output.Header()
	header.Del("Content-Length")
	header.Del("Location")
	header.Set("Cache-Control", "private, no-store")
	clearNavigationReuse(header)
	header.Set("Content-Type", actionview.MediaType+"; charset=utf-8")
	header.Set(actionview.Header, "1")
	header.Set(navigation.ProtocolHeader, fmt.Sprint(navigation.ProtocolVersion))
	header.Set(navigation.ConfigurationHeader, r.app.configurationID)
	r.app.draftResponse(r.output, r.request)
	r.started = true
	r.output.WriteHeader(http.StatusOK)
	return r.frame(actionview.Frame{Type: "result", Result: r.result})
}

func (r *actionViewReply) frame(frame actionview.Frame) error {
	if r.done {
		return fmt.Errorf("action view already completed")
	}
	frame.Version = actionview.Version
	encoded, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if _, err := actionview.DecodeFrame(encoded); err != nil {
		return err
	}
	reserve := 512
	if frame.Type == "done" || frame.Type == "view-error" {
		reserve = 0
	}
	if len(encoded)+1 > actionview.MaxBytes-r.used-reserve || r.count >= actionview.MaxFrames {
		return fmt.Errorf("action view exceeds stream limits")
	}
	r.used += len(encoded) + 1
	r.count++
	if frame.Type == "done" || frame.Type == "view-error" {
		r.done = true
	}
	encoded = append(encoded, '\n')
	n, err := r.output.Write(encoded)
	if err == nil && n != len(encoded) {
		err = io.ErrShortWrite
	}
	if err == nil {
		if flush, ok := r.output.(http.Flusher); ok {
			flush.Flush()
		}
	}
	return err
}

func (r *actionViewReply) fail(status int) {
	if r.done {
		return
	}
	if err := r.start(nil); err != nil {
		return
	}
	_ = r.frame(actionview.Frame{Type: "view-error", Error: &actionview.Failure{Code: "view_failed", Message: "The action succeeded, but refreshed content could not finish. Refresh to reconcile; do not repeat the action.", Status: status}})
}

type actionViewWriter struct {
	reply    *actionViewReply
	header   http.Header
	pagePath string
	status   int
	stream   bool
	buffer   bytes.Buffer
	decoder  navigation.StreamDecoder
	err      error
}

func (w *actionViewWriter) Header() http.Header { return w.header }
func (w *actionViewWriter) WriteHeader(status int) {
	if w.status != 0 || status < 200 {
		return
	}
	w.status = status
	media, _, _ := mime.ParseMediaType(w.header.Get("Content-Type"))
	w.stream = media == navigation.StreamMediaType && status == http.StatusOK
}
func (w *actionViewWriter) Flush() {} // Complete validated frames flush the actual output.
func (w *actionViewWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.stream {
		w.err = w.decoder.Feed(data, func(frame navigation.StreamFrame) error {
			if frame.Type == "shell" {
				if err := w.validatePage(frame.Page); err != nil {
					return err
				}
			}
			headers, err := w.responseHeaders()
			if err != nil {
				return err
			}
			if err := w.reply.start(headers); err != nil {
				return err
			}
			return w.reply.frame(actionview.Frame{Type: frame.Type, Page: frame.Page, ID: frame.ID, HTML: frame.HTML, Failed: frame.Failed})
		})
	} else if len(data) > navigation.MaxStreamFrame-w.buffer.Len() {
		w.err = fmt.Errorf("action view exceeds page limit")
	} else {
		_, w.err = w.buffer.Write(data)
	}
	if w.err != nil {
		return 0, w.err
	}
	return len(data), nil
}
func (w *actionViewWriter) validatePage(data []byte) error {
	var page Envelope
	if wirejson.Unique(data) != nil || wirejson.Strict(data, &page) != nil || page.Version != NavigationProtocolVersion || page.Configuration != w.reply.app.configurationID || page.Build != w.reply.app.config.BuildID || page.Status < 200 || page.Status >= 600 || page.Shell != "" || page.ShellRef || !localURL(page.URL) {
		return fmt.Errorf("invalid fresh action view envelope")
	}
	return nil
}
func (w *actionViewWriter) finish() error {
	if w.err != nil {
		return w.err
	}
	if w.stream {
		return w.decoder.Finish()
	}
	media, _, _ := mime.ParseMediaType(w.header.Get("Content-Type"))
	if media != "application/json" || w.buffer.Len() == 0 {
		return fmt.Errorf("fresh action view did not return a page")
	}
	data := bytes.TrimSpace(w.buffer.Bytes())
	if err := w.validatePage(data); err != nil {
		return err
	}
	headers, err := w.responseHeaders()
	if err != nil {
		return err
	}
	if err := w.reply.start(headers); err != nil {
		return err
	}
	if err := w.reply.frame(actionview.Frame{Type: "shell", Page: data}); err != nil {
		return err
	}
	return w.reply.frame(actionview.Frame{Type: "done"})
}

func (w *actionViewWriter) responseHeaders() (http.Header, error) {
	headers := w.header.Clone()
	cookies, err := actionViewPageCookies(headers.Values("Set-Cookie"), w.pagePath)
	headers["Set-Cookie"] = cookies
	return headers, err
}
