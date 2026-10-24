package navigation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const PurposeHeader = "X-Nextgo-Purpose"
const StreamHeader = "X-Nextgo-Stream"
const StreamVersion = 1
const StreamMediaType = "application/x-nextgo-stream+json"
const MaxStreamFrame = 2 << 20
const MaxStreamBytes = 8 << 20
const MaxStreamFrames = 130 // shell, up to 128 boundaries, done
var streamID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)

type StreamFrame struct {
	Version int             `json:"version"`
	Type    string          `json:"type"`
	Page    json.RawMessage `json:"page,omitempty"`
	ID      string          `json:"id,omitempty"`
	HTML    string          `json:"html,omitempty"`
	Failed  bool            `json:"failed,omitempty"`
}

func ValidStreamID(id string) bool { return streamID.MatchString(id) }

func DecodeStreamFrame(data []byte) (StreamFrame, error) {
	var frame StreamFrame
	if len(data) == 0 || len(data) > MaxStreamFrame || !utf8.Valid(data) {
		return frame, fmt.Errorf("invalid stream frame size or encoding")
	}
	if err := wirejson.Unique(data); err != nil {
		return frame, fmt.Errorf("invalid stream JSON")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return frame, fmt.Errorf("invalid stream object")
	}
	for key := range keys {
		switch key {
		case "version", "type", "page", "id", "html", "failed":
		default:
			return frame, fmt.Errorf("unknown stream field")
		}
	}
	if err := wirejson.Strict(data, &frame); err != nil {
		return frame, fmt.Errorf("invalid stream frame")
	}
	if frame.Version != StreamVersion {
		return frame, fmt.Errorf("unsupported stream version")
	}
	switch frame.Type {
	case "shell":
		if len(frame.Page) == 0 || bytes.Equal(bytes.TrimSpace(frame.Page), []byte("null")) || frame.ID != "" || frame.HTML != "" || frame.Failed {
			return frame, fmt.Errorf("invalid stream shell")
		}
	case "chunk":
		if !streamID.MatchString(frame.ID) || frame.Page != nil {
			return frame, fmt.Errorf("invalid stream boundary")
		}
	case "done":
		if frame.Page != nil || frame.ID != "" || frame.HTML != "" || frame.Failed {
			return frame, fmt.Errorf("invalid stream completion")
		}
	default:
		return frame, fmt.Errorf("unknown stream frame type")
	}
	return frame, nil
}

// StreamDecoder handles arbitrary network byte boundaries. It rejects truncated
// final records and requires one shell, ordered chunks and an explicit done.
type StreamDecoder struct {
	pending       []byte
	total, count  int
	started, done bool
}

func (d *StreamDecoder) Feed(data []byte, emit func(StreamFrame) error) error {
	if len(data) > MaxStreamBytes-d.total {
		return fmt.Errorf("stream exceeds byte limit")
	}
	d.total += len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		take := len(data)
		if end >= 0 {
			take = end
		}
		if take > MaxStreamFrame-len(d.pending) {
			return fmt.Errorf("stream frame exceeds byte limit")
		}
		d.pending = append(d.pending, data[:take]...)
		data = data[take:]
		if end < 0 {
			return nil
		}
		data = data[1:]
		if d.done {
			return fmt.Errorf("stream data follows completion")
		}
		frame, err := DecodeStreamFrame(d.pending)
		if err != nil {
			return err
		}
		d.pending = d.pending[:0]
		d.count++
		if d.count > MaxStreamFrames {
			return fmt.Errorf("stream exceeds frame count")
		}
		if !d.started {
			if frame.Type != "shell" {
				return fmt.Errorf("stream must begin with a shell")
			}
			d.started = true
		} else if frame.Type == "shell" {
			return fmt.Errorf("duplicate stream shell")
		}
		if frame.Type == "done" {
			d.done = true
		}
		if err := emit(frame); err != nil {
			return err
		}
	}
	return nil
}
func (d *StreamDecoder) Finish() error {
	if !d.started || !d.done || len(d.pending) != 0 {
		return fmt.Errorf("incomplete navigation stream")
	}
	return nil
}
