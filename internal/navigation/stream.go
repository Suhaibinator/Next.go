package navigation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const PurposeHeader = "X-Nextgo-Purpose"
const StreamHeader = "X-Nextgo-Stream"
const StreamAdmissionHeader = "X-Nextgo-Stream-Admission"
const StreamVersion = 1
const StreamMediaType = "application/x-nextgo-stream+json"
const MaxStreamFrame = 2 << 20
const MaxStreamBytes = 8 << 20
const MaxStreamFrames = 130 // shell, up to 128 boundaries, done

type StreamFrame struct {
	Version         int             `json:"version"`
	Type            string          `json:"type"`
	Page            json.RawMessage `json:"page,omitempty"`
	ID              string          `json:"id,omitempty"`
	HTML            string          `json:"html,omitempty"`
	Failed          bool            `json:"failed,omitempty"`
	MetadataPending bool            `json:"metadataPending,omitempty"`
	MetadataOwner   string          `json:"metadataOwner,omitempty"`
	MetadataIndices []int           `json:"metadataIndices,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

func ValidStreamID(id string) bool { return len(id) <= 128 && ascii.Identifier(id) }

// MarshalJSON preserves an explicit empty provenance array on pending shells.
func (f StreamFrame) MarshalJSON() ([]byte, error) {
	type frameAlias StreamFrame
	var indices *[]int
	if f.MetadataPending || f.MetadataIndices != nil {
		values := f.MetadataIndices
		if values == nil {
			values = []int{}
		}
		indices = &values
	}
	return json.Marshal(struct {
		frameAlias
		MetadataIndices *[]int `json:"metadataIndices,omitempty"`
	}{frameAlias(f), indices})
}

func DecodeStreamFrame(data []byte) (StreamFrame, error) { return decodeStreamFrame(data, false) }
func DecodeMetadataStreamFrame(data []byte) (StreamFrame, error) {
	return decodeStreamFrame(data, true)
}
func decodeStreamFrame(data []byte, metadata bool) (StreamFrame, error) {
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
		case "metadataPending", "metadataOwner", "metadataIndices", "metadata":
			if !metadata {
				return frame, fmt.Errorf("unnegotiated metadata extension")
			}
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
	_, hasPending := keys["metadataPending"]
	_, hasOwner := keys["metadataOwner"]
	_, hasIndices := keys["metadataIndices"]
	_, hasMetadata := keys["metadata"]
	if frame.Type != "shell" && (hasPending || hasOwner || hasIndices) || frame.Type != "metadata" && hasMetadata {
		return frame, fmt.Errorf("metadata fields on wrong frame")
	}
	switch frame.Type {
	case "shell":
		if len(frame.Page) == 0 || bytes.Equal(bytes.TrimSpace(frame.Page), []byte("null")) || frame.ID != "" || frame.HTML != "" || frame.Failed {
			return frame, fmt.Errorf("invalid stream shell")
		}
		if hasPending || hasOwner || hasIndices {
			raw := bytes.TrimSpace(keys["metadataIndices"])
			if !frame.MetadataPending || !hasPending || !hasOwner || !hasIndices || !ValidMetadataOwner(frame.MetadataOwner) || len(raw) == 0 || raw[0] != '[' || ValidateMetadataIndices(frame.MetadataIndices, int(^uint(0)>>1)) != nil {
				return frame, fmt.Errorf("invalid metadata shell")
			}
			var entries []json.RawMessage
			if json.Unmarshal(raw, &entries) != nil {
				return frame, fmt.Errorf("invalid metadata indices")
			}
			for _, entry := range entries {
				if bytes.Equal(bytes.TrimSpace(entry), []byte("null")) {
					return frame, fmt.Errorf("null metadata index")
				}
			}
		}
	case "metadata":
		if !metadata || len(data) > MaxMetadataBytes || frame.Page != nil || hasStreamField(keys, "id") || hasStreamField(keys, "html") || !hasMetadata {
			return frame, fmt.Errorf("invalid metadata frame")
		}
		if value, exists := keys["failed"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("true")) {
			return frame, fmt.Errorf("invalid metadata failure flag")
		}
		if _, err := DecodeMetadataPayload(frame.Metadata, frame.Failed); err != nil {
			return frame, err
		}
	case "chunk":
		if !ValidStreamID(frame.ID) || frame.Page != nil {
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

func hasStreamField(fields map[string]json.RawMessage, key string) bool {
	_, ok := fields[key]
	return ok
}

// StreamDecoder handles arbitrary network byte boundaries. It rejects truncated
// final records and requires one shell, ordered chunks and an explicit done.
type StreamDecoder struct {
	pending         []byte
	total, count    int
	started, done   bool
	metadata        bool
	metadataOwner   string
	metadataPending bool
}

// NewMetadataStreamDecoder must only be used after an exact response acknowledgment.
func NewMetadataStreamDecoder() *StreamDecoder { return &StreamDecoder{metadata: true} }

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
		frame, err := decodeStreamFrame(d.pending, d.metadata)
		if err != nil {
			return err
		}
		d.pending = d.pending[:0]
		d.count++
		limit := MaxStreamFrames
		if d.metadata {
			limit = MaxMetadataStreamFrames
		}
		if d.count > limit {
			return fmt.Errorf("stream exceeds frame count")
		}
		if !d.started {
			if frame.Type != "shell" {
				return fmt.Errorf("stream must begin with a shell")
			}
			d.started = true
			d.metadataPending = frame.MetadataPending
			d.metadataOwner = frame.MetadataOwner
		} else if frame.Type == "shell" {
			return fmt.Errorf("duplicate stream shell")
		}
		if frame.Type == "metadata" {
			payload, parseErr := DecodeMetadataPayload(frame.Metadata, frame.Failed)
			if parseErr != nil || !d.metadataPending || payload.Owner != d.metadataOwner {
				return fmt.Errorf("unexpected metadata completion")
			}
			d.metadataPending = false
		}
		if frame.Type == "done" {
			if d.metadataPending {
				return fmt.Errorf("stream completed with pending metadata")
			}
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
