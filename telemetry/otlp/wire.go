// Package otlp exports Next.go trace records using OTLP/HTTP JSON and only the
// standard library. Export is bounded and asynchronous; it is not durable storage.
package otlp

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/telemetry"
)

type attribute struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}
type anyValue struct {
	String *string `json:"stringValue,omitempty"`
	Int    *string `json:"intValue,omitempty"`
	Bool   *bool   `json:"boolValue,omitempty"`
}

func text(key, value string) attribute { return attribute{key, anyValue{String: &value}} }
func number(key string, value int64) attribute {
	v := strconv.FormatInt(value, 10)
	return attribute{key, anyValue{Int: &v}}
}

type wireSpan struct {
	TraceID    string      `json:"traceId"`
	SpanID     string      `json:"spanId"`
	ParentID   string      `json:"parentSpanId,omitempty"`
	Name       string      `json:"name"`
	Kind       int         `json:"kind"`
	Flags      int         `json:"flags"`
	Started    string      `json:"startTimeUnixNano"`
	Ended      string      `json:"endTimeUnixNano"`
	Attributes []attribute `json:"attributes,omitempty"`
	Status     struct {
		Code int `json:"code"`
	} `json:"status"`
}
type scopeSpans struct {
	Scope struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"scope"`
	Spans []wireSpan `json:"spans"`
}
type resourceSpans struct {
	Resource struct {
		Attributes []attribute `json:"attributes"`
	} `json:"resource"`
	Scopes []scopeSpans `json:"scopeSpans"`
}
type exportRequest struct {
	Resources []resourceSpans `json:"resourceSpans"`
}

func encode(records []queued, service, version string) ([]byte, error) {
	scope := scopeSpans{}
	scope.Scope.Name = "github.com/Suhaibinator/Next.go"
	scope.Scope.Version = "experimental"
	for _, queued := range records {
		r := queued.record
		span := wireSpan{TraceID: r.TraceID, SpanID: r.SpanID, ParentID: r.ParentID, Name: r.Name, Kind: 1, Flags: 1, Started: strconv.FormatInt(r.Started.UnixNano(), 10), Ended: strconv.FormatInt(r.Started.UnixNano()+int64(r.Duration), 10)}
		if r.Kind == telemetry.Server {
			span.Kind = 2
		}
		if r.Kind == telemetry.Client {
			span.Kind = 3
		}
		if r.Route != "" {
			span.Attributes = append(span.Attributes, text("http.route", r.Route))
		}
		if r.Method != "" {
			span.Attributes = append(span.Attributes, text("http.request.method", r.Method))
		}
		if r.Status != 0 {
			span.Attributes = append(span.Attributes, number("http.response.status_code", int64(r.Status)))
		}
		if r.Bytes != 0 {
			span.Attributes = append(span.Attributes, number("nextgo.response.bytes", r.Bytes))
		}
		if r.Locale != "" {
			span.Attributes = append(span.Attributes, text("nextgo.locale", r.Locale))
		}
		if r.Cache != "" {
			span.Attributes = append(span.Attributes, text("nextgo.cache.state", r.Cache))
		}
		if r.Hijacked {
			v := true
			span.Attributes = append(span.Attributes, attribute{"nextgo.hijacked", anyValue{Bool: &v}})
		}
		if r.Error != "" {
			span.Status.Code = 2
			span.Attributes = append(span.Attributes, text("error.type", string(r.Error)))
		}
		scope.Spans = append(scope.Spans, span)
	}
	resource := resourceSpans{Scopes: []scopeSpans{scope}}
	resource.Resource.Attributes = []attribute{text("service.name", service)}
	if version != "" {
		resource.Resource.Attributes = append(resource.Resource.Attributes, text("service.version", version))
	}
	return json.Marshal(exportRequest{[]resourceSpans{resource}})
}
func validID(value string, length int) bool {
	if len(value) != length {
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
func validText(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func ownRecord(r telemetry.Record) (telemetry.Record, int, error) {
	start := r.Started.UnixNano()
	if !validID(r.TraceID, 32) || !validID(r.SpanID, 16) || r.ParentID != "" && !validID(r.ParentID, 16) || r.Name == "" || !validText(r.Name, 128) || !validText(r.Route, 256) || !validText(r.Method, 32) || !validText(r.Locale, 63) || r.Started.Year() < 1970 || r.Started.Year() > 2262 || start < 0 || r.Duration < 0 || int64(r.Duration) > math.MaxInt64-start || r.Bytes < 0 || r.Status < 0 || r.Status > 999 {
		return r, 0, ErrRecord
	}
	switch r.Kind {
	case telemetry.Internal, telemetry.Server, telemetry.Client:
	default:
		return r, 0, ErrRecord
	}
	switch r.Error {
	case telemetry.ErrorNone, telemetry.ErrorInternal, telemetry.ErrorCanceled, telemetry.ErrorDeadline, telemetry.ErrorPanic, telemetry.ErrorAborted:
	default:
		return r, 0, ErrRecord
	}
	switch r.Cache {
	case "", "miss", "fresh", "stale", "bypass", "disabled":
	default:
		return r, 0, ErrRecord
	}
	switch r.Method {
	case "", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT", "OTHER":
	default:
		return r, 0, ErrRecord
	}
	r.TraceID, r.SpanID, r.ParentID = strings.Clone(r.TraceID), strings.Clone(r.SpanID), strings.Clone(r.ParentID)
	r.Name, r.Route, r.Method, r.Locale = strings.Clone(r.Name), strings.Clone(r.Route), strings.Clone(r.Method), strings.Clone(r.Locale)
	r.Cache = strings.Clone(r.Cache)
	r.Kind = telemetry.Kind(strings.Clone(string(r.Kind)))
	r.Error = telemetry.ErrorKind(strings.Clone(string(r.Error)))
	r.Started = time.Unix(0, start).UTC()
	return r, 384 + len(r.TraceID) + len(r.SpanID) + len(r.ParentID) + len(r.Name) + len(r.Route) + len(r.Method) + len(r.Locale) + len(r.Cache) + len(r.Error), nil
}
