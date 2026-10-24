package shared

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const protocol = 3
const maxWireBytes = 2 << 20

type wireRequest struct {
	Version   int
	Operation string
	ID        ID
	Policy    *Policy        `json:",omitempty"`
	LeaseFor  time.Duration  `json:",omitempty"`
	Token     string         `json:",omitempty"`
	Value     *[]byte        `json:",omitempty"`
	Namespace string         `json:",omitempty"`
	Selector  *Selector      `json:",omitempty"`
	Window    *time.Duration `json:",omitempty"`
	Seeds     *[]SeedValue   `json:",omitempty"`
}
type wireResponse struct {
	Version int
	Error   string       `json:",omitempty"`
	View    *View        `json:",omitempty"`
	Count   *int         `json:",omitempty"`
	Ticket  *SeedTicket  `json:",omitempty"`
	Session *SeedSession `json:",omitempty"`
}

func validSecret(s string) bool {
	if len(s) < 32 || len(s) > 256 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func strict(data []byte, out any) error {
	if len(data) == 0 || data[0] != '{' {
		return ErrProtocol
	}
	if err := wirejson.Unique(data); err != nil {
		return ErrProtocol
	}
	// encoding/json accepts case-insensitive aliases. The protocol does not:
	// Version plus version must not become two ways to set the same field.
	var fields string
	children := map[string]string{}
	switch out.(type) {
	case *wireRequest:
		fields = "Version Operation ID Policy LeaseFor Token Value Namespace Selector Window Seeds"
		children = map[string]string{"ID": "Namespace Schema Key", "Policy": "FreshFor ExpireAfter Tags Paths", "Selector": "Kind Value"}
	case *wireResponse:
		fields = "Version Error View Count Ticket Session"
		children = map[string]string{"View": "Found Value Age FreshFor ExpiresIn NoExpiry Lease LeaseFor Busy", "Ticket": "Token CreatedAt", "Session": "Token LeaseFor"}
	default:
		return ErrProtocol
	}
	if !exactKeys(data, fields, children) {
		return ErrProtocol
	}
	if err := wirejson.Strict(data, out); err != nil {
		return ErrProtocol
	}
	return nil
}
func exactKeys(data []byte, fields string, children map[string]string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return false
	}
	for key, value := range object {
		if !strings.Contains(" "+fields+" ", " "+key+" ") {
			return false
		}
		if fields, ok := children[key]; ok && !bytes.Equal(value, []byte("null")) && !exactKeys(value, fields, nil) {
			return false
		}
		if key == "Seeds" && !bytes.Equal(value, []byte("null")) && !exactSeedArray(value) {
			return false
		}
		if (key == "Tags" || key == "Paths") && !bytes.Equal(value, []byte("null")) && !jsonArrayBound(value, 64, nil) {
			return false
		}
	}
	return true
}
func jsonArrayBound(data []byte, limit int, check func([]byte) bool) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	first, err := d.Token()
	if err != nil || first != json.Delim('[') {
		return false
	}
	n := 0
	for d.More() {
		n++
		if n > limit {
			return false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || check != nil && !check(value) {
			return false
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim(']') {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}
func exactSeedArray(data []byte) bool {
	if len(data) > MaxSeedEncodedBytes {
		return false
	}
	return jsonArrayBound(data, MaxSeedBatch, func(value []byte) bool {
		return exactKeys(value, "ID Policy Value", map[string]string{"ID": "Namespace Schema Key", "Policy": "FreshFor ExpireAfter Tags Paths"})
	})
}
func jsonMedia(header string) bool {
	media, _, err := mime.ParseMediaType(header)
	return err == nil && media == "application/json"
}

// HandlerOptions exposes one namespace through a private authenticated endpoint.
// Use TLS for non-loopback transport and independent secrets per tenant. The
// handler never exposes key enumeration, values or backend diagnostics on errors.
type HandlerOptions struct {
	Backend          Backend
	Namespace, Token string
	MaxConcurrent    int
}
type handler struct {
	backend                  Backend
	namespace, authorization string
	slots                    chan struct{}
}

func NewHandler(o HandlerOptions) (http.Handler, error) {
	if o.Backend == nil || !validName(o.Namespace) || !validSecret(o.Token) || o.MaxConcurrent < 0 || o.MaxConcurrent > 1024 {
		return nil, ErrInvalid
	}
	if o.MaxConcurrent == 0 {
		o.MaxConcurrent = 32
	}
	return &handler{backend: o.Backend, namespace: o.Namespace, authorization: "Bearer " + o.Token, slots: make(chan struct{}, o.MaxConcurrent)}, nil
}
func respond(w http.ResponseWriter, status int, response wireResponse) {
	response.Version = protocol
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// This is a service-to-service endpoint; ambient browser credentials and CORS
	// are not authentication. Reject Origin even with an otherwise valid token.
	if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(h.authorization)) != 1 || len(r.Header.Values("Origin")) != 0 {
		respond(w, 401, wireResponse{Error: "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		respond(w, 405, wireResponse{Error: "invalid"})
		return
	}
	if !jsonMedia(r.Header.Get("Content-Type")) || r.Header.Get("Content-Encoding") != "" {
		respond(w, 415, wireResponse{Error: "invalid"})
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		respond(w, 503, wireResponse{Error: "capacity"})
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWireBytes))
	var req wireRequest
	if err != nil || strict(bytes.TrimSpace(data), &req) != nil || req.Version != protocol {
		respond(w, 400, wireResponse{Error: "invalid"})
		return
	}
	response := wireResponse{}
	noID := req.ID == (ID{})
	noLease := req.Policy == nil && req.LeaseFor == 0
	noValue := req.Token == "" && req.Value == nil
	noSelector := req.Namespace == "" && req.Selector == nil && req.Window == nil
	if req.Seeds != nil && req.Operation != "seed-install" {
		respond(w, 400, wireResponse{Error: "invalid"})
		return
	}
	switch req.Operation {
	case "begin", "begin-stale":
		if req.ID.Namespace != h.namespace || req.Policy == nil || !noValue || !noSelector {
			err = ErrInvalid
			break
		}
		var v View
		if req.Operation == "begin-stale" {
			v, err = h.backend.BeginStale(r.Context(), req.ID, *req.Policy, req.LeaseFor)
		} else {
			v, err = h.backend.Begin(r.Context(), req.ID, *req.Policy, req.LeaseFor)
		}
		response.View = &v
	case "commit":
		if req.ID.Namespace != h.namespace || !noLease || req.Value == nil || !noSelector {
			err = ErrInvalid
			break
		}
		var v View
		v, err = h.backend.Commit(r.Context(), req.ID, req.Token, *req.Value)
		response.View = &v
	case "release", "discard":
		if req.ID.Namespace != h.namespace || !noLease || req.Value != nil || !noSelector {
			err = ErrInvalid
			break
		}
		if req.Operation == "discard" {
			err = h.backend.Discard(r.Context(), req.ID, req.Token)
		} else {
			err = h.backend.Release(r.Context(), req.ID, req.Token)
		}
	case "expire", "mark-stale":
		if !noID || !noLease || !noValue || req.Namespace != h.namespace || req.Selector == nil || req.Operation == "expire" && req.Window != nil || req.Operation == "mark-stale" && req.Window == nil {
			err = ErrInvalid
			break
		}
		var count int
		if req.Operation == "mark-stale" {
			count, err = h.backend.MarkStale(r.Context(), req.Namespace, *req.Selector, *req.Window)
		} else {
			count, err = h.backend.Expire(r.Context(), req.Namespace, *req.Selector)
		}
		response.Count = &count
	case "seed-reserve", "seed-begin", "seed-install", "seed-finish":
		if !noID || !noLease || req.Value != nil || req.Selector != nil || req.Window != nil || req.Namespace != h.namespace || req.Operation == "seed-reserve" && req.Token != "" || req.Operation != "seed-reserve" && ValidateSeedToken(req.Token) != nil || req.Operation == "seed-install" && req.Seeds == nil {
			err = ErrInvalid
			break
		}
		seedBackend, ok := h.backend.(SeedBackend)
		if !ok {
			err = ErrProtocol
			break
		}
		switch req.Operation {
		case "seed-reserve":
			var ticket SeedTicket
			ticket, err = seedBackend.ReserveSeeds(r.Context(), req.Namespace)
			if err == nil && !validSeedTicket(ticket) {
				err = ErrProtocol
			}
			response.Ticket = &ticket
		case "seed-begin":
			var session SeedSession
			session, err = seedBackend.BeginSeeds(r.Context(), req.Namespace, req.Token)
			if err == nil && !validSeedSession(session) {
				err = ErrProtocol
			}
			response.Session = &session
		case "seed-install":
			if err = ValidateSeedValues(req.Namespace, *req.Seeds); err != nil {
				break
			}
			var count int
			count, err = seedBackend.InstallSeeds(r.Context(), req.Namespace, req.Token, *req.Seeds)
			if err == nil && (count < 0 || count > len(*req.Seeds)) {
				err = ErrProtocol
			}
			response.Count = &count
		case "seed-finish":
			err = seedBackend.FinishSeeds(r.Context(), req.Namespace, req.Token)
		}
	default:
		err = ErrInvalid
	}
	if err != nil {
		status, code := 503, "unavailable"
		switch {
		case errors.Is(err, ErrInvalid):
			status, code = 400, "invalid"
		case errors.Is(err, ErrPolicy):
			status, code = 409, "policy"
		case errors.Is(err, ErrCapacity):
			code = "capacity"
		case errors.Is(err, ErrProtocol):
			status, code = 501, "protocol"
		}
		respond(w, status, wireResponse{Error: code})
		return
	}
	respond(w, 200, response)
}

// HTTP implements Backend and optional SeedBackend over protocol v3. Its client is copied, redirects and
// cookie jars are disabled, and response decompression is not requested. A
// failed call is never transparently retried: its remote outcome may be unknown.
type HTTP struct {
	endpoint, token string
	client          http.Client
}

func NewHTTP(endpoint, token string, client *http.Client) (*HTTP, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || u.Opaque != "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !validSecret(token) {
		return nil, ErrInvalid
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, ErrInvalid
	}
	c := http.Client{Timeout: 3 * time.Second}
	if client != nil {
		c = *client
		if c.Timeout <= 0 {
			c.Timeout = 3 * time.Second
		}
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Jar = nil
	return &HTTP{endpoint: u.String(), token: token, client: c}, nil
}
func (h *HTTP) call(ctx context.Context, req wireRequest) (wireResponse, error) {
	if ctx == nil {
		return wireResponse{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return wireResponse{}, err
	}
	req.Version = protocol
	data, err := json.Marshal(req)
	if err != nil || len(data) > maxWireBytes {
		return wireResponse{}, ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(data))
	if err != nil {
		return wireResponse{}, ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+h.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Accept-Encoding", "identity")
	resp, err := h.client.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return wireResponse{}, ctx.Err()
		}
		return wireResponse{}, ErrUnavailable
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxWireBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return wireResponse{}, ctx.Err()
		}
		return wireResponse{}, ErrUnavailable
	}
	var out wireResponse
	if len(data) > maxWireBytes || !jsonMedia(resp.Header.Get("Content-Type")) || resp.Header.Get("Content-Encoding") != "" || strict(bytes.TrimSpace(data), &out) != nil || out.Version != protocol {
		return wireResponse{}, ErrProtocol
	}
	if resp.StatusCode != 200 {
		if out.View != nil || out.Count != nil || out.Ticket != nil || out.Session != nil {
			return wireResponse{}, ErrProtocol
		}
		switch {
		case resp.StatusCode == 400 && out.Error == "invalid":
			return wireResponse{}, ErrInvalid
		case resp.StatusCode == 409 && out.Error == "policy":
			return wireResponse{}, ErrPolicy
		case resp.StatusCode == 503 && out.Error == "capacity":
			return wireResponse{}, ErrCapacity
		case resp.StatusCode == 501 && out.Error == "protocol":
			return wireResponse{}, ErrProtocol
		case resp.StatusCode == 503 && out.Error == "unavailable", resp.StatusCode == 401 && out.Error == "unauthorized":
			return wireResponse{}, ErrUnavailable
		default:
			return wireResponse{}, ErrProtocol
		}
	}
	if out.Error != "" {
		return wireResponse{}, ErrProtocol
	}
	if req.Operation != "seed-reserve" && out.Ticket != nil || req.Operation != "seed-begin" && out.Session != nil {
		return wireResponse{}, ErrProtocol
	}
	switch req.Operation {
	case "begin", "begin-stale", "commit":
		if out.View == nil || out.Count != nil || !validView(*out.View) {
			return wireResponse{}, ErrProtocol
		}
		if req.Operation == "commit" && (out.View.Busy || out.View.Lease != "") {
			return wireResponse{}, ErrProtocol
		}
		if req.Operation == "begin" && out.View.Busy && out.View.Found {
			return wireResponse{}, ErrProtocol
		}
		if req.Operation != "commit" && !out.View.Found && !out.View.Busy && out.View.Lease == "" {
			return wireResponse{}, ErrProtocol
		}
	case "expire", "mark-stale":
		if out.View != nil || out.Count == nil || *out.Count < 0 {
			return wireResponse{}, ErrProtocol
		}
	case "release", "discard":
		if out.View != nil || out.Count != nil {
			return wireResponse{}, ErrProtocol
		}
	case "seed-reserve":
		if out.View != nil || out.Count != nil || out.Ticket == nil || !validSeedTicket(*out.Ticket) {
			return wireResponse{}, ErrProtocol
		}
	case "seed-begin":
		if out.View != nil || out.Count != nil || out.Session == nil || !validSeedSession(*out.Session) {
			return wireResponse{}, ErrProtocol
		}
	case "seed-install":
		if out.View != nil || out.Count == nil || *out.Count < 0 || req.Seeds == nil || *out.Count > len(*req.Seeds) {
			return wireResponse{}, ErrProtocol
		}
	case "seed-finish":
		if out.View != nil || out.Count != nil {
			return wireResponse{}, ErrProtocol
		}
	}
	return out, nil
}
func validSeedTicket(ticket SeedTicket) bool {
	return ValidateSeedToken(ticket.Token) == nil && !ticket.CreatedAt.IsZero() && ticket.CreatedAt.Year() >= 1 && ticket.CreatedAt.Year() <= 9999
}
func validSeedSession(session SeedSession) bool {
	if session.Token == "" {
		return session.LeaseFor == 0
	}
	return ValidateSeedToken(session.Token) == nil && session.LeaseFor > 0 && session.LeaseFor <= SeedSessionTTL
}

func (h *HTTP) ReserveSeeds(ctx context.Context, namespace string) (SeedTicket, error) {
	if ValidateSeedNamespace(namespace) != nil {
		return SeedTicket{}, ErrInvalid
	}
	out, err := h.call(ctx, wireRequest{Operation: "seed-reserve", Namespace: namespace})
	if err != nil {
		return SeedTicket{}, err
	}
	ticket := *out.Ticket
	ticket.CreatedAt = ticket.CreatedAt.UTC()
	return ticket, nil
}
func (h *HTTP) BeginSeeds(ctx context.Context, namespace, ticket string) (SeedSession, error) {
	if ValidateSeedNamespace(namespace) != nil || ValidateSeedToken(ticket) != nil {
		return SeedSession{}, ErrInvalid
	}
	out, err := h.call(ctx, wireRequest{Operation: "seed-begin", Namespace: namespace, Token: ticket})
	if err != nil {
		return SeedSession{}, err
	}
	return *out.Session, nil
}
func (h *HTTP) InstallSeeds(ctx context.Context, namespace, session string, values []SeedValue) (int, error) {
	if ValidateSeedToken(session) != nil || ValidateSeedValues(namespace, values) != nil {
		return 0, ErrInvalid
	}
	if values == nil {
		values = []SeedValue{}
	}
	out, err := h.call(ctx, wireRequest{Operation: "seed-install", Namespace: namespace, Token: session, Seeds: &values})
	if err != nil {
		return 0, err
	}
	return *out.Count, nil
}
func (h *HTTP) FinishSeeds(ctx context.Context, namespace, session string) error {
	if ValidateSeedNamespace(namespace) != nil || ValidateSeedToken(session) != nil {
		return ErrInvalid
	}
	_, err := h.call(ctx, wireRequest{Operation: "seed-finish", Namespace: namespace, Token: session})
	return err
}
func validView(v View) bool {
	if len(v.Value) > MaxValueBytes || v.Age < 0 || v.FreshFor < 0 || v.ExpiresIn < 0 || v.LeaseFor < 0 || v.LeaseFor > time.Minute || v.Lease != "" && !validToken(v.Lease) {
		return false
	}
	if !v.Found && (len(v.Value) != 0 || v.Age != 0 || v.FreshFor != 0 || v.ExpiresIn != 0 || v.NoExpiry) {
		return false
	}
	if v.NoExpiry && v.ExpiresIn != 0 || v.Found && !v.NoExpiry && (v.ExpiresIn <= 0 || v.FreshFor > v.ExpiresIn) {
		return false
	}
	if (v.Busy || v.Lease != "") && v.FreshFor > 0 {
		return false
	}
	return !(v.Busy && v.Lease != "" || (v.Busy || v.Lease != "") != (v.LeaseFor > 0))
}
func (h *HTTP) Begin(ctx context.Context, id ID, p Policy, lease time.Duration) (View, error) {
	return h.begin(ctx, id, p, lease, "begin")
}
func (h *HTTP) BeginStale(ctx context.Context, id ID, p Policy, lease time.Duration) (View, error) {
	return h.begin(ctx, id, p, lease, "begin-stale")
}
func (h *HTTP) begin(ctx context.Context, id ID, p Policy, lease time.Duration, operation string) (View, error) {
	if validateID(id) != nil || validateLease(lease) != nil {
		return View{}, ErrInvalid
	}
	p, err := normalize(p)
	if err != nil {
		return View{}, err
	}
	o, err := h.call(ctx, wireRequest{Operation: operation, ID: id, Policy: &p, LeaseFor: lease})
	if err != nil {
		return View{}, err
	}
	return *o.View, nil
}
func (h *HTTP) Commit(ctx context.Context, id ID, token string, value []byte) (View, error) {
	if validateID(id) != nil || !validToken(token) || len(value) > MaxValueBytes {
		return View{}, ErrInvalid
	}
	o, err := h.call(ctx, wireRequest{Operation: "commit", ID: id, Token: token, Value: &value})
	if err != nil {
		return View{}, err
	}
	return *o.View, nil
}
func (h *HTTP) Release(ctx context.Context, id ID, token string) error {
	return h.retire(ctx, id, token, "release")
}
func (h *HTTP) Discard(ctx context.Context, id ID, token string) error {
	return h.retire(ctx, id, token, "discard")
}
func (h *HTTP) retire(ctx context.Context, id ID, token string, operation string) error {
	if validateID(id) != nil || !validToken(token) {
		return ErrInvalid
	}
	_, err := h.call(ctx, wireRequest{Operation: operation, ID: id, Token: token})
	return err
}
func (h *HTTP) Expire(ctx context.Context, ns string, s Selector) (int, error) {
	return h.change(ctx, ns, s, nil)
}
func (h *HTTP) MarkStale(ctx context.Context, ns string, s Selector, window time.Duration) (int, error) {
	if !validWindow(window) {
		return 0, ErrInvalid
	}
	return h.change(ctx, ns, s, &window)
}
func (h *HTTP) change(ctx context.Context, ns string, s Selector, window *time.Duration) (int, error) {
	s, err := normalizeSelector(ns, s)
	if err != nil {
		return 0, err
	}
	operation := "expire"
	if window != nil {
		operation = "mark-stale"
	}
	o, err := h.call(ctx, wireRequest{Operation: operation, Namespace: ns, Selector: &s, Window: window})
	if err != nil {
		return 0, err
	}
	return *o.Count, nil
}
