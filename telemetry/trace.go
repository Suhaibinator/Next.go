// Package telemetry provides bounded-field server tracing without external
// runtime dependencies. Recorders must be concurrency safe and return promptly.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Kind string

const (
	Internal Kind = "internal"
	Server   Kind = "server"
	Client   Kind = "client"
)

type ErrorKind string

const (
	ErrorNone     ErrorKind = ""
	ErrorInternal ErrorKind = "internal"
	ErrorCanceled ErrorKind = "canceled"
	ErrorDeadline ErrorKind = "deadline"
	ErrorPanic    ErrorKind = "panic"
	ErrorAborted  ErrorKind = "aborted"
)

type Info struct {
	Name   string
	Kind   Kind
	Route  string
	Method string
}
type Result struct {
	Status   int
	Bytes    int64
	Cache    string
	Error    ErrorKind
	Hijacked bool
}

// Record is owned by the recorder. It deliberately contains no raw URL, query,
// parameters, request/response headers, bodies, baggage, or exception message.
type Record struct {
	Name     string        `json:"name"`
	Kind     Kind          `json:"kind"`
	TraceID  string        `json:"traceId"`
	SpanID   string        `json:"spanId"`
	ParentID string        `json:"parentId,omitempty"`
	Started  time.Time     `json:"started"`
	Duration time.Duration `json:"durationNanos"`
	Route    string        `json:"route,omitempty"`
	Method   string        `json:"method,omitempty"`
	Locale   string        `json:"locale,omitempty"`
	Status   int           `json:"status,omitempty"`
	Bytes    int64         `json:"bytes,omitempty"`
	Cache    string        `json:"cache,omitempty"`
	Error    ErrorKind     `json:"error,omitempty"`
	Hijacked bool          `json:"hijacked,omitempty"`
}

type Options struct {
	Recorder func(Record) error
	// Flush and Shutdown optionally own an asynchronous recorder's lifecycle.
	// Hooks must honor context cancellation. Apps flush before Lambda freeze
	// and shut down after completion work drains; share ownership deliberately.
	Flush    func(context.Context) error
	Shutdown func(context.Context) error
	// SampleEvery uses a deterministic 1/N root decision; zero defaults to one.
	// Trusted remote parents preserve their sampled bit. Nil tracing disables it.
	SampleEvery uint64
	// TrustIncoming accepts valid version-00 traceparent headers. Enable only
	// at a trusted ingress. tracestate and baggage are not propagated.
	TrustIncoming bool
}

type Tracer struct {
	options            Options
	recorded, failures atomic.Uint64
	shutdownMu         sync.Mutex
	shutdownDone       chan struct{}
	shutdownErr        error
}
type Stats struct{ Recorded, Failures uint64 }

func New(options Options) (*Tracer, error) {
	if options.Recorder == nil || options.SampleEvery > 1<<20 {
		return nil, fmt.Errorf("telemetry: recorder required; SampleEvery must be 0..1048576")
	}
	if options.SampleEvery == 0 {
		options.SampleEvery = 1
	}
	return &Tracer{options: options}, nil
}
func (t *Tracer) Stats() Stats {
	if t == nil {
		return Stats{}
	}
	return Stats{t.recorded.Load(), t.failures.Load()}
}

var ErrLifecycle = errors.New("telemetry: recorder lifecycle failed")

func lifecycle(ctx context.Context, hook func(context.Context) error) (err error) {
	if ctx == nil {
		return ErrLifecycle
	}
	if hook == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = ErrLifecycle
		}
	}()
	return hook(ctx)
}
func (t *Tracer) Flush(ctx context.Context) error {
	if t == nil {
		return nil
	}
	err := lifecycle(ctx, t.options.Flush)
	if err != nil {
		t.failures.Add(1)
	}
	return err
}
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	if ctx == nil {
		return ErrLifecycle
	}
	t.shutdownMu.Lock()
	if t.shutdownDone != nil {
		done := t.shutdownDone
		t.shutdownMu.Unlock()
		select {
		case <-done:
			t.shutdownMu.Lock()
			err := t.shutdownErr
			t.shutdownMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.shutdownDone = make(chan struct{})
	done := t.shutdownDone
	t.shutdownMu.Unlock()
	hook := t.options.Shutdown
	if hook == nil {
		hook = t.options.Flush
	}
	err := lifecycle(ctx, hook)
	if err != nil {
		t.failures.Add(1)
	}
	t.shutdownMu.Lock()
	t.shutdownErr = err
	close(done)
	t.shutdownMu.Unlock()
	return err
}

type SpanContext struct {
	TraceID, SpanID string
	Sampled         bool
}
type tracerKey struct{}
type spanKey struct{}
type requestKey struct{}
type requestState struct {
	tracer        *Tracer
	mu            sync.Mutex
	route, locale string
}
type spanState struct {
	tracer   *Tracer
	identity SpanContext
}
type Span struct {
	tracer  *Tracer
	ctx     context.Context
	record  Record
	sampled bool
	ended   atomic.Bool
	mu      sync.Mutex
	cache   string
}

func Context(ctx context.Context) SpanContext {
	if ctx == nil {
		return SpanContext{}
	}
	v, _ := ctx.Value(spanKey{}).(spanState)
	return v.identity
}
func Active(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	t, _ := ctx.Value(tracerKey{}).(*Tracer)
	return t != nil
}
func (t *Tracer) Start(ctx context.Context, info Info) (context.Context, *Span) {
	if t == nil || ctx == nil {
		return ctx, nil
	}
	parent := Context(ctx)
	identity := SpanContext{TraceID: parent.TraceID, Sampled: parent.Sampled}
	if !validID(parent.TraceID, 32) || !validID(parent.SpanID, 16) {
		bytes := make([]byte, 16)
		_, _ = rand.Read(bytes)
		identity.TraceID = hex.EncodeToString(bytes)
		identity.Sampled = binary.BigEndian.Uint64(bytes[:8])%t.options.SampleEvery == 0
		parent = SpanContext{}
	}
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	identity.SpanID = hex.EncodeToString(bytes)
	if info.Kind != Server && info.Kind != Client {
		info.Kind = Internal
	}
	ctx = context.WithValue(ctx, tracerKey{}, t)
	ctx = context.WithValue(ctx, spanKey{}, spanState{t, identity})
	span := &Span{tracer: t, ctx: ctx, sampled: identity.Sampled, record: Record{Name: bounded(info.Name, 128), Kind: info.Kind, TraceID: identity.TraceID, SpanID: identity.SpanID, ParentID: parent.SpanID, Started: time.Now(), Route: bounded(info.Route, 256), Method: method(info.Method)}}
	if span.record.Name == "" {
		span.record.Name = "app.span"
	}
	return ctx, span
}
func Start(ctx context.Context, info Info) (context.Context, *Span) {
	if ctx == nil {
		return ctx, nil
	}
	t, _ := ctx.Value(tracerKey{}).(*Tracer)
	return t.Start(ctx, info)
}
func (s *Span) End(result Result) {
	if s == nil || !s.ended.CompareAndSwap(false, true) || !s.sampled {
		return
	}
	record := s.record
	record.Duration = time.Since(record.Started)
	record.Status, record.Bytes, record.Error, record.Hijacked = result.Status, max(0, result.Bytes), result.Error, result.Hijacked
	if record.Error == ErrorNone {
		record.Error = Classify(s.ctx.Err())
	}
	if record.Error == ErrorNone && record.Status >= 500 {
		record.Error = ErrorInternal
	}
	switch record.Error {
	case ErrorNone, ErrorInternal, ErrorCanceled, ErrorDeadline, ErrorPanic, ErrorAborted:
	default:
		record.Error = ErrorInternal
	}
	if state, _ := s.ctx.Value(requestKey{}).(*requestState); state != nil {
		state.mu.Lock()
		if record.Route == "" {
			record.Route = state.route
		}
		record.Locale = state.locale
		state.mu.Unlock()
	}
	s.mu.Lock()
	record.Cache = s.cache
	s.mu.Unlock()
	if result.Cache != "" {
		record.Cache = cacheState(result.Cache)
	}
	defer func() {
		if recover() != nil {
			s.tracer.failures.Add(1)
		}
	}()
	if err := s.tracer.options.Recorder(record); err != nil {
		s.tracer.failures.Add(1)
		return
	}
	s.tracer.recorded.Add(1)
}
func (s *Span) Cache(state string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cache = cacheState(state)
	s.mu.Unlock()
}
func cacheState(state string) string {
	switch state {
	case "miss", "fresh", "stale", "bypass", "disabled":
		return state
	}
	return ""
}
func Classify(err error) ErrorKind {
	if err == nil {
		return ErrorNone
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorDeadline
	}
	if errors.Is(err, context.Canceled) {
		return ErrorCanceled
	}
	return ErrorInternal
}
func SetRoute(ctx context.Context, route string) {
	if ctx == nil {
		return
	}
	if s, _ := ctx.Value(requestKey{}).(*requestState); s != nil {
		s.mu.Lock()
		s.route = bounded(route, 256)
		s.mu.Unlock()
	}
}
func SetLocale(ctx context.Context, locale string) {
	if ctx == nil {
		return
	}
	if s, _ := ctx.Value(requestKey{}).(*requestState); s != nil {
		s.mu.Lock()
		s.locale = bounded(locale, 63)
		s.mu.Unlock()
	}
}

// Run traces cooperative work and records panic/Goexit without swallowing it.
func Run(ctx context.Context, name string, run func(context.Context) error) (err error) {
	if !Active(ctx) {
		return run(ctx)
	}
	ctx, span := Start(ctx, Info{Name: name})
	completed := false
	defer func() {
		result := Result{Error: Classify(err)}
		if !completed {
			result.Error = ErrorAborted
		}
		if failure := recover(); failure != nil {
			span.End(Result{Error: ErrorPanic})
			panic(failure)
		}
		span.End(result)
	}()
	err = run(ctx)
	completed = true
	return err
}

func method(value string) string {
	switch value {
	case "", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE":
		return value
	}
	return "OTHER"
}

// JSONRecorder serializes records to a caller-owned writer. It does no network
// work and preserves encoder failures for Tracer.Stats. The writer stays open.
func JSONRecorder(writer io.Writer) func(Record) error {
	var mu sync.Mutex
	return func(record Record) error { mu.Lock(); defer mu.Unlock(); return json.NewEncoder(writer).Encode(record) }
}
func bounded(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) && len(value) > 0 {
			value = value[:len(value)-1]
		}
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
}
func validID(value string, size int) bool {
	if len(value) != size {
		return false
	}
	nonzero := false
	for _, c := range value {
		if c != '0' {
			nonzero = true
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return nonzero
}

// ParseTraceparent accepts exactly W3C version 00. Invalid, zero or future
// versions start a new trace; this is not full Trace Context conformance.
func ParseTraceparent(value string) (SpanContext, bool) {
	if len(value) != 55 || value[:3] != "00-" || value[35] != '-' || value[52] != '-' || !validID(value[3:35], 32) || !validID(value[36:52], 16) {
		return SpanContext{}, false
	}
	flags, err := hex.DecodeString(value[53:])
	if err != nil || strings.ToLower(value[53:]) != value[53:] {
		return SpanContext{}, false
	}
	return SpanContext{value[3:35], value[36:52], flags[0]&1 == 1}, true
}
func Traceparent(ctx context.Context) string {
	identity := Context(ctx)
	if !validID(identity.TraceID, 32) || !validID(identity.SpanID, 16) {
		return ""
	}
	flags := "00"
	if identity.Sampled {
		flags = "01"
	}
	return "00-" + identity.TraceID + "-" + identity.SpanID + "-" + flags
}
