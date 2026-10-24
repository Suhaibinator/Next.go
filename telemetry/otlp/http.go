package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxResponse = 64 << 10
const maxPayload = 4 << 20

func (e *Exporter) send(parent context.Context, batch []queued) (accepted int, warning bool, failure error) {
	failure = ErrExport
	defer func() {
		if recover() != nil {
			accepted = 0
			warning = false
			failure = ErrExport
		}
	}()
	payload, err := encode(batch, e.options.ServiceName, e.options.ServiceVersion)
	if err != nil || len(payload) > maxPayload {
		return 0, false, ErrExport
	}
	ctx, cancel := context.WithTimeout(parent, e.options.Timeout)
	defer cancel()
	stop := context.AfterFunc(e.root, cancel)
	defer stop()
	if e.root.Err() != nil {
		cancel()
	}
	for attempt := 0; attempt < e.options.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return 0, false, ctx.Err()
		}
		if attempt > 0 {
			e.mu.Lock()
			e.stats.Retries++
			e.mu.Unlock()
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.options.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return 0, false, ErrExport
		}
		request.Header = e.options.Headers.Clone()
		if request.Header == nil {
			request.Header = make(http.Header)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		response, err := e.client.Do(request)
		retry := err != nil
		delay := time.Duration(50+rand.IntN(101)) * time.Millisecond * time.Duration(1<<attempt)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
			closeErr := response.Body.Close()
			if response.StatusCode == 200 {
				contentType, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
				if readErr != nil || closeErr != nil {
					retry = true
				} else if len(body) > maxResponse || typeErr != nil || contentType != "application/json" {
					return 0, false, ErrExport
				} else {
					var reply struct {
						Partial *struct {
							Rejected decimal `json:"rejectedSpans"`
							Message  string  `json:"errorMessage"`
						} `json:"partialSuccess"`
					}
					if json.Unmarshal(body, &reply) != nil {
						return 0, false, ErrExport
					}
					if reply.Partial == nil {
						return len(batch), false, nil
					}
					rejected := int64(reply.Partial.Rejected)
					if rejected < 0 || rejected > int64(len(batch)) {
						return 0, false, ErrExport
					}
					if rejected > 0 {
						return len(batch) - int(rejected), true, ErrExport
					}
					return len(batch), reply.Partial.Message != "", nil
				}
			} else {
				switch response.StatusCode {
				case 429, 502, 503, 504:
					retry = true
					if after, ok := retryAfter(response.Header.Get("Retry-After")); ok {
						delay = after
					}
				default:
					return 0, false, ErrExport
				}
			}
		}
		if !retry || attempt+1 >= e.options.MaxAttempts {
			return 0, false, ErrExport
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, false, ctx.Err()
		case <-timer.C:
		}
	}
	return 0, false, ErrExport
}

type decimal int64

func (n *decimal) UnmarshalJSON(raw []byte) error {
	value := string(raw)
	if value == "null" {
		*n = 0
		return nil
	}
	if strings.HasPrefix(value, "\"") {
		if json.Unmarshal(raw, &value) != nil {
			return ErrExport
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return ErrExport
	}
	*n = decimal(parsed)
	return nil
}
func retryAfter(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= 86400 {
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(when)), true
	}
	return 0, false
}
