package otlp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/telemetry"
)

var (
	ErrRecord    = errors.New("otlp: invalid or oversized trace record")
	ErrQueueFull = errors.New("otlp: trace queue capacity exhausted")
	ErrClosed    = errors.New("otlp: exporter closed")
	ErrFlushBusy = errors.New("otlp: too many concurrent flushes")
	ErrExport    = errors.New("otlp: trace export failed; inspect exporter counters")
)

type Options struct {
	Endpoint, ServiceName, ServiceVersion            string
	Headers                                          http.Header
	Transport                                        http.RoundTripper
	QueueSize, MaxQueueBytes, BatchSize, MaxAttempts int
	Interval, Timeout                                time.Duration
}
type Stats struct {
	Accepted, Exported, Dropped, FailedBatches, Retries, Warnings uint64
	Queued, QueueBytes, InFlight                                  int
}
type queued struct {
	sequence uint64
	record   telemetry.Record
	size     int
}
type flushRequest struct {
	ctx    context.Context
	target uint64
	done   chan error
}
type Exporter struct {
	options             Options
	client              *http.Client
	owned               *http.Transport
	root                context.Context
	cancel              context.CancelFunc
	mu                  sync.Mutex
	queue               []queued
	sequence, completed uint64
	stats               Stats
	closed              bool
	closing             context.Context
	closeErr            error
	wake                chan struct{}
	flush               chan flushRequest
	done                chan struct{}
}

func New(options Options) (*Exporter, error) {
	u, err := url.Parse(options.Endpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || len(options.Endpoint) > 8192 || strings.ContainsAny(options.Endpoint, "\\\r\n#") {
		return nil, fmt.Errorf("otlp: Endpoint requires an HTTP(S) URL without credentials, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("otlp: invalid endpoint port")
		}
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	options.Endpoint = u.String()
	if options.ServiceName == "" || !validText(options.ServiceName, 128) || !validText(options.ServiceVersion, 128) {
		return nil, fmt.Errorf("otlp: service name required; resource fields must be at most 128 bytes")
	}
	if options.QueueSize == 0 {
		options.QueueSize = 1024
	}
	if options.MaxQueueBytes == 0 {
		options.MaxQueueBytes = 4 << 20
	}
	if options.BatchSize == 0 {
		options.BatchSize = min(64, options.QueueSize)
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = 3
	}
	if options.Interval == 0 {
		options.Interval = time.Second
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.QueueSize < 1 || options.QueueSize > 65536 || options.MaxQueueBytes < 1024 || options.MaxQueueBytes > 64<<20 || options.BatchSize < 1 || options.BatchSize > 512 || options.BatchSize > options.QueueSize || options.MaxAttempts < 1 || options.MaxAttempts > 5 || options.Interval < time.Millisecond || options.Interval > time.Minute || options.Timeout < time.Millisecond || options.Timeout > time.Minute {
		return nil, fmt.Errorf("otlp: invalid queue/batch/attempt/time budget")
	}
	total := 0
	for key, values := range options.Headers {
		if !headerName(key) {
			return nil, fmt.Errorf("otlp: invalid collector header name")
		}
		switch strings.ToLower(key) {
		case "host", "content-length", "content-type", "transfer-encoding", "connection", "traceparent", "tracestate", "baggage", "proxy-authorization":
			return nil, fmt.Errorf("otlp: reserved collector header")
		}
		total += len(key)
		for _, v := range values {
			total += len(v)
			if len(v) > 4096 || strings.ContainsAny(v, "\r\n") || !validHeaderValue(v) {
				return nil, fmt.Errorf("otlp: invalid collector header value")
			}
		}
	}
	if len(options.Headers) > 32 || total > 16384 {
		return nil, fmt.Errorf("otlp: collector headers exceed bounds")
	}
	ownedHeaders := make(http.Header, len(options.Headers))
	for key, values := range options.Headers {
		for _, value := range values {
			ownedHeaders.Add(strings.Clone(key), strings.Clone(value))
		}
	}
	options.Headers = ownedHeaders
	options.ServiceName = strings.Clone(options.ServiceName)
	options.ServiceVersion = strings.Clone(options.ServiceVersion)
	e := &Exporter{options: options, wake: make(chan struct{}, 1), flush: make(chan flushRequest, 16), done: make(chan struct{})}
	e.root, e.cancel = context.WithCancel(context.Background())
	transport := options.Transport
	if transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			base = &http.Transport{}
		}
		e.owned = base.Clone()
		e.owned.Proxy = nil
		transport = e.owned
	}
	e.client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	go e.worker()
	return e, nil
}
func headerName(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
func validHeaderValue(value string) bool {
	for _, c := range value {
		if c < 32 && c != '\t' || c == 127 {
			return false
		}
	}
	return true
}
func (e *Exporter) Record(record telemetry.Record) error {
	record, size, err := ownRecord(record)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if len(e.queue) >= e.options.QueueSize || size > e.options.MaxQueueBytes-e.stats.QueueBytes {
		e.stats.Dropped++
		return ErrQueueFull
	}
	e.sequence++
	e.queue = append(e.queue, queued{e.sequence, record, size})
	e.stats.Accepted++
	e.stats.Queued = len(e.queue)
	e.stats.QueueBytes += size
	if len(e.queue) >= e.options.BatchSize {
		e.signal()
	}
	return nil
}
func (e *Exporter) Stats() Stats { e.mu.Lock(); defer e.mu.Unlock(); return e.stats }
func (e *Exporter) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Flush processes records accepted before this call. A prior automatic export
// keeps its independent batch timeout; flush-owned batches honor ctx's deadline.
// Failures during processing return ErrExport; prior losses remain in Stats.
func (e *Exporter) Flush(ctx context.Context) error {
	if ctx == nil {
		return errors.New("otlp: Flush requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	target := e.sequence
	closed := e.closed
	completed := e.completed
	e.mu.Unlock()
	if completed >= target {
		return nil
	}
	if closed {
		return ErrClosed
	}
	request := flushRequest{ctx: ctx, target: target, done: make(chan error, 1)}
	select {
	case e.flush <- request:
	case <-e.done:
		return ErrClosed
	default:
		return ErrFlushBusy
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return ErrClosed
	}
}

// Shutdown seals admission and drains accepted spans. The first caller owns the
// drain deadline. An expired deadline cancels export and drops remaining work.
func (e *Exporter) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("otlp: Shutdown requires a context")
	}
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		e.closing = ctx
		context.AfterFunc(ctx, e.cancel)
		e.signal()
	}
	e.mu.Unlock()
	select {
	case <-e.done:
		e.mu.Lock()
		err := e.closeErr
		e.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *Exporter) take(target uint64, partial bool) []queued {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := min(len(e.queue), e.options.BatchSize)
	if !partial && n < e.options.BatchSize {
		return nil
	}
	if target > 0 {
		for n > 0 && e.queue[n-1].sequence > target {
			n--
		}
	}
	if n == 0 {
		return nil
	}
	batch := append([]queued(nil), e.queue[:n]...)
	for i := 0; i < n; i++ {
		e.stats.QueueBytes -= e.queue[i].size
		e.queue[i] = queued{}
	}
	e.queue = e.queue[n:]
	e.stats.Queued = len(e.queue)
	e.stats.InFlight = n
	return batch
}
func (e *Exporter) process(ctx context.Context, batch []queued) error {
	if len(batch) == 0 {
		return nil
	}
	accepted, warnings, err := e.send(ctx, batch)
	e.mu.Lock()
	e.completed = batch[len(batch)-1].sequence
	e.stats.InFlight = 0
	e.stats.Exported += uint64(accepted)
	e.stats.Dropped += uint64(len(batch) - accepted)
	if warnings {
		e.stats.Warnings++
	}
	if err != nil {
		e.stats.FailedBatches++
	}
	e.mu.Unlock()
	return err
}
func (e *Exporter) handleFlush(request flushRequest) {
	var result error
	for {
		if err := request.ctx.Err(); err != nil {
			result = err
			break
		}
		e.mu.Lock()
		completed := e.completed
		e.mu.Unlock()
		if completed >= request.target {
			break
		}
		batch := e.take(request.target, true)
		if len(batch) == 0 {
			break
		}
		if err := e.process(request.ctx, batch); err != nil {
			result = err
		}
	}
	request.done <- result
}
func (e *Exporter) worker() {
	ticker := time.NewTicker(e.options.Interval)
	defer ticker.Stop()
	defer func() {
		failure := recover()
		e.cancel()
		if e.owned != nil {
			e.owned.CloseIdleConnections()
		}
		e.mu.Lock()
		remaining := len(e.queue) + e.stats.InFlight
		if failure != nil || remaining > 0 {
			if e.closeErr == nil {
				e.closeErr = ErrExport
				if e.closing != nil && e.closing.Err() != nil {
					e.closeErr = e.closing.Err()
				}
			}
		}
		e.stats.Dropped += uint64(remaining)
		e.queue = nil
		e.stats.Queued = 0
		e.stats.QueueBytes = 0
		e.stats.InFlight = 0
		e.completed = e.sequence
		e.closed = true
		e.mu.Unlock()
		close(e.done)
	}()
	for {
		if e.root.Err() != nil {
			return
		}
		e.mu.Lock()
		closing := e.closing
		e.mu.Unlock()
		if closing != nil {
			batch := e.take(0, true)
			if len(batch) == 0 {
				return
			}
			if err := e.process(closing, batch); err != nil {
				e.mu.Lock()
				e.closeErr = err
				e.mu.Unlock()
			}
			continue
		}
		select {
		case request := <-e.flush:
			e.handleFlush(request)
			continue
		default:
		}
		if batch := e.take(0, false); len(batch) > 0 {
			_ = e.process(e.root, batch)
			continue
		}
		select {
		case <-e.root.Done():
			return
		case request := <-e.flush:
			e.handleFlush(request)
		case <-e.wake:
		case <-ticker.C:
			if batch := e.take(0, true); len(batch) > 0 {
				_ = e.process(e.root, batch)
			}
		}
	}
}
