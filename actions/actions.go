// Package actions provides explicitly registered server mutations for Go browser
// components and progressively enhanced HTML forms. Handlers must perform their
// own authentication, authorization, and application-level validation.
package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/accesscontrol"
	"github.com/Suhaibinator/Next.go/internal/actionview"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Return these results before a protected mutation. Messages are generic and
// wrapped errors never enter JSON or progressive HTML responses.
var ErrUnauthorized = accesscontrol.Unauthorized
var ErrForbidden = accesscontrol.Forbidden

const defaultMaxBodyBytes = 1 << 20

// Options controls request limits, trusted origins and successful invalidation.
// AllowedOrigins are exact HTTP(S) origins, including a port when needed. A
// reverse proxy must preserve Host or explicitly configure its public origin;
// forwarded headers are never trusted automatically.
type Options struct {
	// UnauthorizedChallenge optionally supplies WWW-Authenticate for 401 results.
	UnauthorizedChallenge string
	MaxBodyBytes          int64
	AllowedOrigins        []string
	OnRevalidate          func(tags, paths []string)
	LogError              func(*http.Request, error)
}

type Result struct {
	Data            any      `json:"data,omitempty"`
	Redirect        string   `json:"redirect,omitempty"`
	RevalidateTags  []string `json:"-"`
	RevalidatePaths []string `json:"-"`
}

type Handler func(*Context) (Result, error)

// ValidationError is safe to show to the requester. Never put secrets in it.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	if e.Message == "" {
		return "Please check the submitted values."
	}
	return e.Message
}

// Error describes a public failure in the JSON response.
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type Response struct {
	Data     any    `json:"data,omitempty"`
	Redirect string `json:"redirect,omitempty"`
	Error    *Error `json:"error,omitempty"`
}

type Context struct {
	context.Context
	Request      *http.Request
	maxBodyBytes int64
}

type requestError struct {
	status        int
	code, message string
}

func (e *requestError) Error() string { return e.message }

// DecodeJSON accepts one JSON value, rejects unknown struct fields and extra
// values, and observes the router's request body limit.
func (c *Context) DecodeJSON(dst any) error {
	media, _, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
		return &requestError{http.StatusUnsupportedMediaType, "unsupported_media_type", "Expected a JSON request body."}
	}
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return bodyError(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return bodyError(err)
		}
		return &requestError{http.StatusBadRequest, "invalid_body", "Expected one JSON value."}
	}
	return nil
}

// ParseForm parses URL-encoded or multipart form bodies and returns body values
// only (query parameters are not merged). Uploaded files are available through
// Request.MultipartForm until the handler returns, then removed automatically.
func (c *Context) ParseForm() (url.Values, error) {
	media, _, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if err != nil {
		return nil, &requestError{415, "unsupported_media_type", "Expected a form request body."}
	}
	switch media {
	case "application/x-www-form-urlencoded":
		err = c.Request.ParseForm()
	case "multipart/form-data":
		err = c.Request.ParseMultipartForm(c.maxBodyBytes)
	default:
		return nil, &requestError{415, "unsupported_media_type", "Expected a form request body."}
	}
	if err != nil {
		return nil, bodyError(err)
	}
	return c.Request.PostForm, nil
}

func bodyError(err error) error {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		return &requestError{413, "body_too_large", "The request body is too large."}
	}
	return &requestError{400, "invalid_body", "The request body is invalid."}
}

// Router is safe for concurrent registration and serving. Register handlers
// before exposing it to traffic so callers see a stable action contract.
type Router struct {
	options  Options
	origins  map[string]bool
	mu       sync.RWMutex
	handlers map[string]registration
}

type registration struct {
	handler  Handler
	revision string
}

func New(options Options) *Router {
	options = cloneOptions(options)
	if len(options.UnauthorizedChallenge) > 8192 {
		panic("actions: unauthorized challenge exceeds 8 KiB")
	}
	for _, value := range options.UnauthorizedChallenge {
		if value < 32 || value > 126 {
			panic("actions: unauthorized challenge must contain printable ASCII")
		}
	}

	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = defaultMaxBodyBytes
	}
	if options.LogError == nil {
		options.LogError = func(_ *http.Request, err error) { log.Printf("nextgo action: %v", err) }
	}
	r := &Router{options: options, origins: map[string]bool{}, handlers: map[string]registration{}}
	for _, origin := range options.AllowedOrigins {
		key, ok := originKey(origin)
		if !ok {
			panic(fmt.Sprintf("actions: invalid allowed origin %q", origin))
		}
		r.origins[key] = true
	}
	return r
}

// Handle registers an action name relative to the router's mount path. Names
// are ASCII identifier segments separated by slashes. Invalid/duplicate names
// and nil handlers panic, as with http.ServeMux.Handle.
func (r *Router) Handle(name string, handler Handler) {
	r.register(name, "", handler)
}

// HandleContract registers protocol/revision metadata for a typed endpoint.
// Names should include their revision so stale clients miss before mutation.
func (r *Router) HandleContract(name, revision string, handler Handler) {
	if len(revision) > 64 || !actionwire.ValidSegment(revision) {
		panic("actions: invalid contract revision")
	}
	r.register(name, revision, handler)
}

func (r *Router) register(name, revision string, handler Handler) {
	if !validName(name) || handler == nil {
		panic("actions: invalid action registration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[name]; exists {
		panic("actions: duplicate action " + name)
	}
	r.handlers[name] = registration{handler: handler, revision: revision}
}

func validName(name string) bool {
	return actionwire.ValidName(name)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.serve(w, req, nil, false)
}

func (r *Router) serve(w http.ResponseWriter, req *http.Request, render func(http.ResponseWriter, *http.Request, ViewSuccess), view bool) {
	var tracked *viewResponseWriter
	if view {
		tracked = &viewResponseWriter{ResponseWriter: w}
		w = tracked
	}
	w.Header().Set(actionwire.ProtocolHeader, actionwire.Protocol)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "Accept")
	defer func() {
		if recovered := recover(); recovered != nil {
			r.options.LogError(req, fmt.Errorf("panic: %v", recovered))
			if !view || !tracked.started {
				message := "The action could not be completed."
				if view {
					message = viewUnknownOutcome
				}
				r.fail(w, req, 500, &Error{Code: "internal_error", Message: message})
			}
		}
		if req.MultipartForm != nil {
			_ = req.MultipartForm.RemoveAll()
		}
	}()
	if view && render == nil {
		r.options.LogError(req, errors.New("actions: ServeWithView requires a renderer"))
		r.fail(w, req, 500, &Error{Code: "internal_error", Message: viewUnknownOutcome})
		return
	}
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		r.fail(w, req, 405, &Error{Code: "method_not_allowed", Message: "Actions require POST."})
		return
	}
	if !r.sameOrigin(req) {
		r.fail(w, req, 403, &Error{Code: "origin_rejected", Message: "The request origin is not allowed."})
		return
	}
	name := strings.TrimPrefix(req.URL.Path, "/")
	r.mu.RLock()
	entry := r.handlers[name]
	r.mu.RUnlock()
	if entry.handler == nil || !validName(name) {
		r.fail(w, req, 404, &Error{Code: "not_found", Message: "The action was not found."})
		return
	}
	if entry.revision != "" {
		w.Header().Set(actionwire.RevisionHeader, entry.revision)
	}
	if protocol := req.Header.Get(actionwire.ProtocolHeader); protocol != "" && protocol != actionwire.Protocol {
		r.fail(w, req, 409, &Error{Code: "protocol_mismatch", Message: "The action protocol changed. Reload before retrying."})
		return
	}
	if revision := req.Header.Get(actionwire.RevisionHeader); revision != "" && revision != entry.revision {
		r.fail(w, req, 409, &Error{Code: "contract_mismatch", Message: "The action contract changed. Reload before retrying."})
		return
	}
	if req.ContentLength > r.options.MaxBodyBytes {
		r.fail(w, req, 413, &Error{Code: "body_too_large", Message: "The request body is too large."})
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, r.options.MaxBodyBytes)
	defer req.Body.Close()
	// Verify the entire bounded input before a user handler can mutate state.
	// This also covers chunked requests and handlers that do not read the body.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		r.handleError(w, req, bodyError(err))
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	result, err := entry.handler(&Context{Context: req.Context(), Request: req, maxBodyBytes: r.options.MaxBodyBytes})
	if err != nil {
		r.handleError(w, req, err)
		return
	}
	if result.Redirect != "" && !safeRedirect(result.Redirect) {
		r.resultError(w, req, errors.New("action returned an unsafe redirect"), view)
		return
	}
	if result.Redirect != "" {
		result.Redirect, err = paths.FromContext(req.Context()).URL(result.Redirect)
		if err != nil {
			r.resultError(w, req, err, view)
			return
		}
	}
	// Encode before committing headers or invoking callbacks. Unsupported data
	// types produce a generic error instead of a partial JSON response.
	response := Response{Data: result.Data, Redirect: result.Redirect}
	encoded, err := json.Marshal(response)
	if err != nil {
		r.resultError(w, req, err, view)
		return
	}
	if r.options.OnRevalidate != nil && (len(result.RevalidateTags) > 0 || len(result.RevalidatePaths) > 0) {
		r.options.OnRevalidate(append([]string(nil), result.RevalidateTags...), append([]string(nil), result.RevalidatePaths...))
	}
	if view {
		if len(encoded) > actionview.MaxResult {
			r.resultError(w, req, errors.New("actions: successful action view result exceeds byte limit; mutation may have committed"), true)
			return
		}
		if err := wirejson.Unique(encoded); err != nil {
			r.resultError(w, req, errors.New("actions: successful action view result contains ambiguous JSON; mutation may have committed"), true)
			return
		}
		render(w, req, ViewSuccess{Encoded: append([]byte(nil), encoded...), Redirect: result.Redirect, RevalidateTags: append([]string(nil), result.RevalidateTags...), RevalidatePaths: append([]string(nil), result.RevalidatePaths...)})
		return
	}
	if wantsJSON(req) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(200)
		_, _ = w.Write(append(encoded, '\n'))
		return
	}
	if result.Redirect != "" {
		http.Redirect(w, req, result.Redirect, http.StatusSeeOther)
		return
	}
	r.html(w, req, 200, "Action completed", "Your changes were saved.", nil)
}

func (r *Router) handleError(w http.ResponseWriter, req *http.Request, err error) {
	var validation *ValidationError
	var badRequest *requestError
	var large *http.MaxBytesError
	switch {
	case errors.Is(err, ErrForbidden):
		r.fail(w, req, http.StatusForbidden, &Error{Code: "forbidden", Message: "You do not have permission to perform this action."})
	case errors.Is(err, ErrUnauthorized):
		r.fail(w, req, http.StatusUnauthorized, &Error{Code: "unauthorized", Message: "Authentication is required to perform this action."})
	case errors.As(err, &validation):
		r.fail(w, req, 422, &Error{Code: "validation_error", Message: validation.Error(), Fields: validation.Fields})
	case errors.As(err, &badRequest):
		r.fail(w, req, badRequest.status, &Error{Code: badRequest.code, Message: badRequest.message})
	case errors.As(err, &large):
		r.fail(w, req, 413, &Error{Code: "body_too_large", Message: "The request body is too large."})
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		r.fail(w, req, 408, &Error{Code: "request_canceled", Message: "The request was canceled. Please try again."})
	default:
		r.options.LogError(req, err)
		r.fail(w, req, 500, &Error{Code: "internal_error", Message: "The action could not be completed."})
	}
}

func wantsJSON(req *http.Request) bool {
	for _, item := range strings.Split(req.Header.Get("Accept"), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err == nil && media == "application/json" && params["q"] != "0" {
			return true
		}
	}
	media, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	return media == "application/json" || strings.HasSuffix(media, "+json")
}

func (r *Router) fail(w http.ResponseWriter, req *http.Request, status int, failure *Error) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		if status == http.StatusUnauthorized && r.options.UnauthorizedChallenge != "" {
			w.Header().Set("WWW-Authenticate", r.options.UnauthorizedChallenge)
		}
	}

	if wantsJSON(req) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(Response{Error: failure})
		return
	}
	r.html(w, req, status, http.StatusText(status), failure.Message, failure.Fields)
}

var messageTemplate = template.Must(template.New("action").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title></head><body><main><h1>{{.Title}}</h1><p role="status">{{.Message}}</p>{{if .Fields}}<ul>{{range $name, $message := .Fields}}<li><strong>{{$name}}</strong>: {{$message}}</li>{{end}}</ul>{{end}}<p><a href="{{.Home}}">Return to the application</a></p></main></body></html>`))

func (r *Router) html(w http.ResponseWriter, req *http.Request, status int, title, message string, fields map[string]string) {
	home, err := paths.FromContext(req.Context()).URL("/")
	if err != nil {
		home = "/"
	}
	var b bytes.Buffer
	_ = messageTemplate.Execute(&b, struct {
		Title, Message string
		Home           string
		Fields         map[string]string
	}{title, message, home, fields})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = b.WriteTo(w)
}

func originKey(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		host = strings.TrimSuffix(host, ":"+u.Port())
	}
	return u.Scheme + "://" + host, true
}

func (r *Router) sameOrigin(req *http.Request) bool {
	if len(req.Header.Values("Origin")) > 1 || len(req.Header.Values("Sec-Fetch-Site")) > 1 {
		return false
	}
	if strings.EqualFold(req.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	expected, ok := originKey(scheme + "://" + req.Host)
	if !ok {
		return false
	}
	allowed := func(raw string) bool {
		origin, valid := originKey(raw)
		return valid && (origin == expected || r.origins[origin])
	}
	if origin := req.Header.Get("Origin"); origin != "" {
		return allowed(origin)
	}
	if referer := req.Header.Get("Referer"); referer != "" {
		u, err := url.Parse(referer)
		if err != nil || u.User != nil {
			return false
		}
		return allowed(u.Scheme + "://" + u.Host)
	}
	return req.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func safeRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" || !strings.HasPrefix(raw, "/") {
		return false
	}
	// Check decoded paths too: URL-encoded backslashes and authority markers
	// must not become external navigation through a browser or proxy rewrite.
	for _, value := range []string{raw, u.Path} {
		if strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\\r\n\t") {
			return false
		}
		for _, ch := range value {
			if ch < 32 || ch == 127 {
				return false
			}
		}
	}
	return true
}
