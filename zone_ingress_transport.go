package nextgo

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

const zoneIngressHeaderBytes = 64 << 10

type zoneIngressHead struct {
	status   int
	headers  http.Header
	trailers []string
	body     bool
	err      error
}
type zoneIngressChunk struct {
	data     []byte
	trailers http.Header
	done     bool
	err      error
}

func zoneIngressTransport(options ZoneIngressOptions) *http.Transport {
	base := options.Transport
	if base == nil {
		base, _ = http.DefaultTransport.(*http.Transport)
	}
	if base == nil {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DisableCompression = true
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport.Protocols = protocols
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	transport.ResponseHeaderTimeout = options.Timeout
	transport.MaxResponseHeaderBytes = zoneIngressHeaderBytes
	transport.MaxConnsPerHost = options.MaxInFlight
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: options.Timeout, KeepAlive: -1}).DialContext
	}
	dialTLS, legacyTLS := transport.DialTLSContext, transport.DialTLS
	transport.DialContext = zoneIngressDial(dial)
	transport.DialTLS = nil
	transport.DialTLSContext = zoneIngressDial(func(ctx context.Context, network, address string) (net.Conn, error) {
		if dialTLS != nil {
			return dialTLS(ctx, network, address)
		}
		if legacyTLS != nil {
			return legacyTLS(network, address)
		}
		connection, err := dial(ctx, network, address)
		if err != nil {
			if connection != nil {
				_ = connection.Close()
			}
			return nil, err
		}
		config := transport.TLSClientConfig.Clone()
		if config.ServerName == "" {
			config.ServerName, _, err = net.SplitHostPort(address)
			if err != nil {
				_ = connection.Close()
				return nil, err
			}
		}
		secure := tls.Client(connection, config)
		// Handshake callbacks run under the dial lease. Transport's own TLS
		// timeout wrapper can otherwise return before user callbacks finish.
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = secure.Close()
			return nil, err
		}
		return secure, nil
	})
	return transport
}

type zoneIngressPhysicalKey struct{}
type zoneIngressPhysical struct {
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	sealed       bool
	count        int
	idle         chan struct{}
	conns        map[*zoneIngressConn]struct{}
	headerBuffer []byte
	headerBytes  int
	headerDone   bool
	headerErr    error
	hopHeader    http.Header
}

func newZoneIngressPhysical(ctx context.Context) *zoneIngressPhysical {
	ctx, cancel := context.WithCancel(ctx)
	idle := make(chan struct{})
	close(idle)
	return &zoneIngressPhysical{ctx: ctx, cancel: cancel, idle: idle, conns: map[*zoneIngressConn]struct{}{}}
}
func (physical *zoneIngressPhysical) begin() bool {
	physical.mu.Lock()
	defer physical.mu.Unlock()
	if physical.sealed {
		return false
	}
	physical.addLocked()
	return true
}
func (physical *zoneIngressPhysical) addLocked() {
	if physical.count == 0 {
		physical.idle = make(chan struct{})
	}
	physical.count++
}
func (physical *zoneIngressPhysical) end() {
	physical.mu.Lock()
	physical.count--
	if physical.count == 0 {
		close(physical.idle)
	}
	physical.mu.Unlock()
}
func (physical *zoneIngressPhysical) own(connection net.Conn) net.Conn {
	owned := &zoneIngressConn{Conn: connection, physical: physical}
	physical.mu.Lock()
	physical.addLocked() // Keep the connection charged until physical Close returns.
	physical.conns[owned] = struct{}{}
	sealed := physical.sealed
	physical.mu.Unlock()
	if sealed {
		go owned.Close()
	}
	return owned
}
func (physical *zoneIngressPhysical) join() {
	physical.mu.Lock()
	physical.sealed = true
	connections := make([]*zoneIngressConn, 0, len(physical.conns))
	for connection := range physical.conns {
		connections = append(connections, connection)
	}
	idle := physical.idle
	physical.mu.Unlock()
	physical.cancel()
	for _, connection := range connections {
		go connection.Close()
	}
	<-idle
}

// net/http removes Connection when it contains "close", losing its nominated
// field names. Capture only that bounded HTTP/1 metadata before the authoritative
// Transport parser consumes it. Body framing and all other response semantics
// continue to come exclusively from http.Transport.
func (physical *zoneIngressPhysical) observeHeaders(data []byte) {
	physical.mu.Lock()
	defer physical.mu.Unlock()
	if physical.headerDone || physical.headerErr != nil {
		return
	}
	remaining := zoneIngressHeaderBytes - physical.headerBytes - len(physical.headerBuffer)
	take := len(data)
	if take > remaining {
		take = remaining
	}
	physical.headerBuffer = append(physical.headerBuffer, data[:take]...)
	for {
		index := bytes.Index(physical.headerBuffer, []byte("\r\n\r\n"))
		if index < 0 {
			if take < len(data) || remaining == take {
				physical.headerErr = errors.New("upstream header budget")
			}
			return
		}
		block := physical.headerBuffer[:index+4]
		reader := textproto.NewReader(bufio.NewReader(bytes.NewReader(block)))
		line, err := reader.ReadLine()
		fields := strings.SplitN(line, " ", 3)
		if err != nil || len(fields) < 2 || fields[0] != "HTTP/1.1" && fields[0] != "HTTP/1.0" {
			physical.headerErr = errors.New("invalid upstream header framing")
			return
		}
		status, err := strconv.Atoi(fields[1])
		if err != nil {
			physical.headerErr = errors.New("invalid upstream status")
			return
		}
		header, err := reader.ReadMIMEHeader()
		if err != nil {
			physical.headerErr = errors.New("invalid upstream header framing")
			return
		}
		physical.headerBytes += len(block)
		if status >= 100 && status < 200 && status != 101 {
			physical.headerBuffer = append([]byte(nil), physical.headerBuffer[index+4:]...)
			continue
		}
		physical.hopHeader = http.Header(header)
		physical.headerDone = true
		physical.headerBuffer = nil
		return
	}
}
func (physical *zoneIngressPhysical) responseHopHeaders() (http.Header, error) {
	physical.mu.Lock()
	defer physical.mu.Unlock()
	if !physical.headerDone || physical.headerErr != nil {
		return nil, errors.New("upstream header metadata unavailable")
	}
	return physical.hopHeader.Clone(), nil
}
func zoneIngressDial(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		physical, _ := ctx.Value(zoneIngressPhysicalKey{}).(*zoneIngressPhysical)
		if physical == nil || !physical.begin() {
			return nil, context.Canceled
		}
		defer physical.end()
		// Transport can detach dial cancellation to reuse an eventual connection.
		// This ingress cannot reuse it, so retain the request's actual cancellation.
		work, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(physical.ctx, cancel)
		defer stop()
		defer cancel()
		if physical.ctx.Err() != nil {
			return nil, physical.ctx.Err()
		}
		connection, err := dial(work, network, address)
		if connection != nil {
			connection = physical.own(connection)
			if err != nil || physical.ctx.Err() != nil {
				_ = connection.Close()
				return nil, context.Canceled
			}
		}
		return connection, err
	}
}

type zoneIngressConn struct {
	net.Conn
	physical *zoneIngressPhysical
	once     sync.Once
	err      error
}

func (connection *zoneIngressConn) Read(data []byte) (int, error) {
	if !connection.physical.begin() {
		return 0, net.ErrClosed
	}
	defer connection.physical.end()
	count, err := connection.Conn.Read(data)
	if count > 0 {
		connection.physical.observeHeaders(data[:count])
	}
	return count, err
}
func (connection *zoneIngressConn) Write(data []byte) (int, error) {
	if !connection.physical.begin() {
		return 0, net.ErrClosed
	}
	defer connection.physical.end()
	return connection.Conn.Write(data)
}
func (connection *zoneIngressConn) Close() error {
	connection.once.Do(func() {
		defer connection.physical.end()
		defer func() {
			if recover() != nil {
				connection.err = errors.New("upstream close failed")
			}
		}()
		defer func() {
			connection.physical.mu.Lock()
			delete(connection.physical.conns, connection)
			connection.physical.mu.Unlock()
		}()
		connection.err = connection.Conn.Close()
	})
	return connection.err
}
func (connection *zoneIngressConn) deadline(run func() error) error {
	if !connection.physical.begin() {
		return net.ErrClosed
	}
	defer connection.physical.end()
	return run()
}
func (connection *zoneIngressConn) SetDeadline(value time.Time) error {
	return connection.deadline(func() error { return connection.Conn.SetDeadline(value) })
}
func (connection *zoneIngressConn) SetReadDeadline(value time.Time) error {
	return connection.deadline(func() error { return connection.Conn.SetReadDeadline(value) })
}
func (connection *zoneIngressConn) SetWriteDeadline(value time.Time) error {
	return connection.deadline(func() error { return connection.Conn.SetWriteDeadline(value) })
}

func zoneIngressPrivateHeader(name string) bool {
	return strings.HasPrefix(strings.TrimPrefix(strings.ToLower(name), strings.ToLower(http.TrailerPrefix)), "x-nextgo-zone-")
}
func zoneIngressConnectionToken(header http.Header, want string) bool {
	for key, values := range header {
		if strings.EqualFold(key, "Connection") {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					if strings.EqualFold(strings.TrimSpace(token), want) {
						return true
					}
				}
			}
		}
	}
	return false
}
func zoneIngressForbiddenConnection(header http.Header) bool {
	for name, values := range header {
		if strings.EqualFold(name, "Connection") {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					key := strings.ToLower(strings.TrimSpace(token))
					if strings.HasPrefix(key, "x-nextgo-") || key == "origin" || key == "referer" || key == "authorization" || key == "cookie" || key == "set-cookie" || key == "host" {
						return true
					}
				}
			}
		}
	}
	return false
}
func zoneIngressHeadersBounded(header http.Header) bool {
	if len(header) > 1024 {
		return false
	}
	total := 0
	for key, values := range header {
		if len(key) > zoneIngressHeaderBytes-total {
			return false
		}
		total += len(key)
		for _, value := range values {
			if len(value) > zoneIngressHeaderBytes-total {
				return false
			}
			total += len(value)
		}
	}
	return total <= zoneIngressHeaderBytes
}

func zoneIngressCleanHeaders(input http.Header) http.Header {
	header := input.Clone()
	remove := map[string]bool{"connection": true, "proxy-connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true, "te": true, "trailer": true, "transfer-encoding": true, "upgrade": true}
	for name, values := range input {
		if strings.EqualFold(name, "Connection") {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					remove[strings.ToLower(strings.TrimSpace(token))] = true
				}
			}
		}
	}
	for name := range header {
		lower := strings.ToLower(name)
		if remove[lower] || lower == "forwarded" || lower == "x-forwarded" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded-") || zoneIngressPrivateHeader(name) {
			delete(header, name)
		}
	}
	zonecontract.StripExpectation(header)
	return header
}

// The cancellation callback and direct close share one physical close. Joining
// a started callback prevents admission from being released while Close blocks.
func zoneIngressBodyCloser(ctx context.Context, body io.ReadCloser) func() {
	var once sync.Once
	closeBody := func() {
		once.Do(func() {
			defer func() { _ = recover() }()
			_ = body.Close()
		})
	}
	settled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(settled); closeBody() })
	var finish sync.Once
	return func() {
		finish.Do(func() {
			if stop() {
				closeBody()
			} else {
				<-settled
			}
		})
	}
}

func (ingress *ZoneIngress) dispatch(ctx context.Context, incoming *http.Request, route zoneIngressRoute, ready chan<- zoneIngressHead, chunks chan<- zoneIngressChunk, downstreamDone <-chan struct{}) {
	// The upstream may finish before a slow or uncooperative public writer.
	// Admission covers both physical halves of this request.
	defer func() {
		<-downstreamDone
		ingress.release()
	}()
	failed := func(status int, err error) { ready <- zoneIngressHead{status: status, err: err} }
	published := false
	defer func() {
		if recover() != nil {
			if !published {
				failed(http.StatusBadGateway, errors.New("upstream operation failed"))
			} else {
				select {
				case chunks <- zoneIngressChunk{err: errors.New("upstream operation failed")}:
				case <-ctx.Done():
				}
			}
		}
	}()
	var finishRequest func()
	if incoming.Body != nil && incoming.Body != http.NoBody {
		finishRequest = zoneIngressBodyCloser(ctx, incoming.Body)
		defer finishRequest()
	}
	var body []byte
	if incoming.ContentLength > ingress.options.MaxRequestBytes {
		failed(http.StatusRequestEntityTooLarge, errors.New("request byte limit"))
		return
	}
	if incoming.Body != nil && incoming.Body != http.NoBody {
		finish := finishRequest
		var err error
		body, err = io.ReadAll(io.LimitReader(incoming.Body, ingress.options.MaxRequestBytes+1))
		// Make the result available before joining a possibly blocking Close.
		if int64(len(body)) > ingress.options.MaxRequestBytes {
			failed(http.StatusRequestEntityTooLarge, errors.New("request byte limit"))
			finish()
			return
		}
		if err != nil || ctx.Err() != nil {
			status := http.StatusBadRequest
			if ctx.Err() != nil {
				status = http.StatusRequestTimeout
			}
			failed(status, errors.New("request body read failed"))
			finish()
			return
		}
		finish()
	}
	if ctx.Err() != nil {
		failed(http.StatusGatewayTimeout, ctx.Err())
		return
	}
	target := &url.URL{Scheme: route.release.origin.Scheme, Host: route.release.origin.Host, Path: incoming.URL.Path, RawPath: incoming.URL.RawPath, RawQuery: incoming.URL.RawQuery, ForceQuery: incoming.URL.ForceQuery}
	request := incoming.Clone(ctx)
	request.URL, request.Host, request.RequestURI = target, target.Host, ""
	request.Header = zoneIngressCleanHeaders(incoming.Header)
	zonecontract.SetExpectation(request.Header, route.release.spec.Expectation())
	request.Body, request.GetBody = http.NoBody, nil
	if len(body) > 0 {
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	request.ContentLength = int64(len(body))
	request.Trailer, request.TransferEncoding, request.Close = nil, nil, true
	physical := newZoneIngressPhysical(ctx)
	defer physical.join()
	request = request.WithContext(context.WithValue(physical.ctx, zoneIngressPhysicalKey{}, physical))
	response, err := ingress.transport.RoundTrip(request)
	if err != nil {
		status := http.StatusBadGateway
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
		}
		failed(status, errors.New("upstream transport failed"))
		return
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	finish := zoneIngressBodyCloser(ctx, response.Body)
	defer finish()
	bodyAllowed := incoming.Method != http.MethodHead && response.StatusCode != http.StatusNotModified && response.StatusCode != http.StatusNoContent
	invalid := response.StatusCode < 200 || response.StatusCode > 599 || response.Header.Get("Upgrade") != "" || zoneIngressConnectionToken(response.Header, "upgrade") || zoneIngressForbiddenConnection(response.Header) || zonecontract.CheckExpectation(response.Header, route.release.spec.Expectation()) != nil || !zoneIngressHeadersBounded(response.Header)
	for name := range response.Trailer {
		invalid = invalid || zoneIngressPrivateHeader(name) || route.asset && strings.EqualFold(name, "Set-Cookie")
	}
	if route.asset && len(response.Header.Values("Set-Cookie")) != 0 || bodyAllowed && response.ContentLength > ingress.options.MaxResponseBytes {
		invalid = true
	}
	if invalid {
		failed(http.StatusBadGateway, errors.New("upstream release response rejected"))
		return
	}
	hopHeader, hopErr := physical.responseHopHeaders()
	if hopErr != nil || zoneIngressForbiddenConnection(hopHeader) {
		failed(http.StatusBadGateway, errors.New("upstream hop metadata rejected"))
		return
	}
	headers := zoneIngressCleanHeaders(response.Header)
	// The raw metadata never establishes identity or body framing. It supplies
	// only Connection-nominated names discarded by Transport's close handling.
	zoneIngressRemoveNominated(headers, hopHeader)
	trailerNames := zoneIngressCleanHeaders(response.Trailer)
	zoneIngressRemoveNominated(trailerNames, hopHeader)
	declared := make([]string, 0, len(trailerNames))
	for name := range trailerNames {
		declared = append(declared, name)
	}
	ready <- zoneIngressHead{status: response.StatusCode, headers: headers, trailers: declared, body: bodyAllowed}
	published = true
	send := func(chunk zoneIngressChunk) bool {
		select {
		case chunks <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !bodyAllowed {
		send(zoneIngressChunk{done: true})
		return
	}
	buffer := make([]byte, 32<<10)
	remaining := ingress.options.MaxResponseBytes
	for {
		if ctx.Err() != nil {
			return
		}
		read := buffer
		if int64(len(read)) > remaining+1 {
			read = read[:remaining+1]
		}
		count, err := response.Body.Read(read)
		if int64(count) > remaining {
			send(zoneIngressChunk{err: errors.New("upstream response byte limit")})
			return
		}
		remaining -= int64(count)
		if count > 0 && !send(zoneIngressChunk{data: append([]byte(nil), read[:count]...)}) {
			return
		}
		if err != nil {
			if err != io.EOF || !zoneIngressHeadersBounded(response.Trailer) {
				send(zoneIngressChunk{err: errors.New("upstream response read failed")})
				return
			}
			trailers := zoneIngressCleanHeaders(response.Trailer)
			zoneIngressRemoveNominated(trailers, hopHeader)
			if route.asset && len(trailers.Values("Set-Cookie")) != 0 {
				send(zoneIngressChunk{err: errors.New("upstream asset cookie rejected")})
				return
			}
			send(zoneIngressChunk{done: true, trailers: trailers})
			return
		}
	}
}

func zoneIngressRemoveNominated(header, hop http.Header) {
	for name := range header {
		if zoneIngressConnectionToken(hop, name) {
			delete(header, name)
		}
	}
}
