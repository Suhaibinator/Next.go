// Package actionview owns the bounded mutation-result/current-view stream. It
// shares no server or browser runtime and does not authorize view destinations.
package actionview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const Version = 1
const Header = "X-Nextgo-Action-View"
const MediaType = "application/x-nextgo-action-view+json"
const URLHeader = "X-Nextgo-Action-URL"
const BuildHeader = "X-Nextgo-Action-Build"
const MaxResult = 64 << 10
const MaxURL = 8 << 10
const MaxFrame = 2 << 20
const MaxBytes = 8 << 20
const MaxFrames = 131 // result, shell, up to 128 boundary chunks, done

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

type Frame struct {
	Version int             `json:"version"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result,omitempty"`
	Page    json.RawMessage `json:"page,omitempty"`
	ID      string          `json:"id,omitempty"`
	HTML    string          `json:"html,omitempty"`
	Failed  bool            `json:"failed,omitempty"`
	Error   *Failure        `json:"error,omitempty"`
}

// DecodeFrame validates exact field names and frame-local shape. The page
// envelope and shell/boundary references still need normal navigation and DOM
// validation by their owners before any browser view can commit.
func DecodeFrame(data []byte) (Frame, error) {
	var frame Frame
	if len(data) == 0 || len(data) > MaxFrame || !utf8.Valid(data) {
		return frame, fmt.Errorf("invalid action view frame size or encoding")
	}
	if err := wirejson.Unique(data); err != nil {
		return frame, fmt.Errorf("invalid action view JSON")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil || keys == nil {
		return frame, fmt.Errorf("expected action view object")
	}
	for key, value := range keys {
		switch key {
		case "version", "type", "result", "page", "id", "html", "failed", "error":
		default:
			return frame, fmt.Errorf("unknown action view field")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return frame, fmt.Errorf("null action view field")
		}
	}
	if err := wirejson.Strict(data, &frame); err != nil {
		return frame, fmt.Errorf("invalid action view frame")
	}
	if frame.Version != Version {
		return frame, fmt.Errorf("unsupported action view version")
	}
	allowed := map[string]bool{"version": true, "type": true}
	switch frame.Type {
	case "result":
		allowed["result"] = true
		if err := validateResult(frame.Result); err != nil {
			return frame, err
		}
	case "shell":
		allowed["page"] = true
		page := bytes.TrimSpace(frame.Page)
		if len(page) == 0 || page[0] != '{' {
			return frame, fmt.Errorf("expected action view page object")
		}
		if err := validateNavigation(frame); err != nil {
			return frame, err
		}
	case "chunk":
		allowed["id"], allowed["html"], allowed["failed"] = true, true, true
		if err := validateNavigation(frame); err != nil {
			return frame, err
		}
	case "done":
		if err := validateNavigation(frame); err != nil {
			return frame, err
		}
	case "view-error":
		allowed["error"] = true
		if frame.Error == nil || !actionwire.ValidSegment(frame.Error.Code) || len(frame.Error.Code) > 64 || frame.Error.Message == "" || len(frame.Error.Message) > 4096 || frame.Error.Status < 400 || frame.Error.Status > 599 {
			return frame, fmt.Errorf("invalid action view failure")
		}
		var failureKeys map[string]json.RawMessage
		if err := json.Unmarshal(keys["error"], &failureKeys); err != nil {
			return frame, fmt.Errorf("invalid action view failure object")
		}
		for _, key := range []string{"code", "message", "status"} {
			value, ok := failureKeys[key]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return frame, fmt.Errorf("missing or null action view failure field")
			}
		}
	default:
		return frame, fmt.Errorf("unknown action view frame type")
	}
	for key := range keys {
		if !allowed[key] {
			return frame, fmt.Errorf("unexpected action view %s field", frame.Type)
		}
	}
	return frame, nil
}

func validateNavigation(frame Frame) error {
	// Reuse the existing version-one stream shape and boundary identifier
	// checks. The outer action protocol additionally rejects present zero/null
	// fields inappropriate for each frame type.
	data, err := json.Marshal(navigation.StreamFrame{Version: navigation.StreamVersion, Type: frame.Type, Page: frame.Page, ID: frame.ID, HTML: frame.HTML, Failed: frame.Failed})
	if err != nil {
		return fmt.Errorf("invalid action view navigation frame")
	}
	if _, err := navigation.DecodeStreamFrame(data); err != nil {
		return fmt.Errorf("invalid action view navigation frame: %w", err)
	}
	return nil
}

func validateResult(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > MaxResult {
		return fmt.Errorf("action result exceeds byte limit or is missing")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return fmt.Errorf("expected action result object")
	}
	for key, value := range values {
		switch key {
		case "data": // Nullable success values retain their typed contract policy.
		case "redirect":
			var redirect string
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &redirect) != nil || len(redirect) > MaxURL {
				return fmt.Errorf("invalid action result redirect")
			}
			if redirect != "" {
				if _, err := (paths.Config{}).URL(redirect); err != nil {
					return fmt.Errorf("unsafe action result redirect")
				}
			}
		default:
			return fmt.Errorf("unknown action result field")
		}
	}
	return nil
}

// Decoder accepts newline-delimited frames across arbitrary network byte
// boundaries. A confirmed result always comes first; one shell/chunks/done or a
// terminal view-error follows. A terminal view-error may also follow a partial
// view. It never changes the fact that the first mutation result was received.
// Every error is latched; a rejected or truncated stream cannot be resumed.
type Decoder struct {
	pending             []byte
	total, count        int
	result, shell, done bool
	chunks              map[string]bool
	err                 error
}

func (d *Decoder) fail(err error) error {
	if d.err == nil {
		d.err = err
	}
	return d.err
}

func (d *Decoder) Feed(data []byte, emit func(Frame) error) error {
	if d.err != nil {
		return d.err
	}
	if len(data) > MaxBytes-d.total {
		return d.fail(fmt.Errorf("action view stream exceeds byte limit"))
	}
	d.total += len(data)
	for len(data) > 0 {
		if d.done {
			return d.fail(fmt.Errorf("action view data follows completion"))
		}
		end := bytes.IndexByte(data, '\n')
		take := len(data)
		if end >= 0 {
			take = end
		}
		if take > MaxFrame-len(d.pending) {
			return d.fail(fmt.Errorf("action view frame exceeds byte limit"))
		}
		d.pending = append(d.pending, data[:take]...)
		data = data[take:]
		if end < 0 {
			return nil
		}
		data = data[1:]
		frame, err := DecodeFrame(d.pending)
		if err != nil {
			return d.fail(err)
		}
		d.pending = d.pending[:0]
		d.count++
		if d.count > MaxFrames {
			return d.fail(fmt.Errorf("action view stream exceeds frame count"))
		}
		if !d.result {
			if frame.Type != "result" {
				return d.fail(fmt.Errorf("action view stream must begin with a result"))
			}
			d.result = true
		} else {
			switch frame.Type {
			case "view-error":
				d.done = true
			case "shell":
				if d.shell {
					return d.fail(fmt.Errorf("duplicate action view shell"))
				}
				d.shell = true
			case "chunk":
				if !d.shell {
					return d.fail(fmt.Errorf("action view chunk precedes shell"))
				}
				if d.chunks == nil {
					d.chunks = map[string]bool{}
				}
				if d.chunks[frame.ID] {
					return d.fail(fmt.Errorf("duplicate action view boundary"))
				}
				if len(d.chunks) >= 128 {
					return d.fail(fmt.Errorf("action view exceeds boundary limit"))
				}
				d.chunks[frame.ID] = true
			case "done":
				if !d.shell {
					return d.fail(fmt.Errorf("action view completion precedes shell"))
				}
				d.done = true
			default:
				return d.fail(fmt.Errorf("duplicate action view result"))
			}
		}
		if emit != nil {
			if err := emit(frame); err != nil {
				return d.fail(err)
			}
		}
	}
	return nil
}

func (d *Decoder) Finish() error {
	if d.err != nil {
		return d.err
	}
	if !d.result || !d.done || len(d.pending) != 0 {
		return d.fail(fmt.Errorf("incomplete action view stream"))
	}
	return nil
}
