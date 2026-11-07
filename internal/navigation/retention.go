package navigation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const RetentionVersion = 1
const RetentionAttribute = "data-nextgo-retention"
const MaxRetentionHTML = 1 << 20
const MaxRetentionDescriptor = 1024
const MaxRetentionPartition = 128

var ErrRetention = errors.New("nextgo: invalid route retention descriptor")

// Retention binds retained browser ownership to one exact server rendering.
// Partition is public application metadata, not an authorization credential.
type Retention struct {
	Version     int    `json:"version"`
	Partition   string `json:"partition"`
	Fingerprint string `json:"fingerprint"`
}

func retentionPartition(value string) bool {
	if len(value) < 1 || len(value) > MaxRetentionPartition {
		return false
	}
	for i := range value {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// NewRetention leaves oversized valid HTML on the ordinary fresh-render path.
func NewRetention(partition, html string) (*Retention, error) {
	if !retentionPartition(partition) {
		return nil, ErrRetention
	}
	if len(html) > MaxRetentionHTML {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(html))
	value := &Retention{Version: RetentionVersion, Partition: strings.Clone(partition), Fingerprint: hex.EncodeToString(sum[:])}
	if err := value.ValidateDescriptor(); err != nil {
		return nil, err
	}
	return value, nil
}

// ValidateDescriptor checks bootstrap metadata without serializing browser DOM.
// Browser HTML parsing can normalize the original server markup. Fresh return
// navigation must additionally call Validate on its exact raw envelope HTML.
func (r *Retention) ValidateDescriptor() error {
	if r == nil || r.Version != RetentionVersion || !retentionPartition(r.Partition) || len(r.Fingerprint) != sha256.Size*2 {
		return ErrRetention
	}
	for i := range r.Fingerprint {
		value := r.Fingerprint[i]
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			return ErrRetention
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxRetentionDescriptor {
		return ErrRetention
	}
	return nil
}

func (r *Retention) Validate(html string) error {
	if r.ValidateDescriptor() != nil || len(html) > MaxRetentionHTML {
		return ErrRetention
	}
	sum := sha256.Sum256([]byte(html))
	if r.Fingerprint != hex.EncodeToString(sum[:]) {
		return ErrRetention
	}
	return nil
}

// ParseRetentionDescriptor strictly decodes bootstrap metadata. It rejects
// unknown, aliased, duplicate, null and trailing fields before shape validation.
func ParseRetentionDescriptor(raw string) (*Retention, error) {
	if raw == "" {
		return nil, nil
	}
	data := []byte(raw)
	if len(data) > MaxRetentionDescriptor || !utf8.Valid(data) || wirejson.Unique(data) != nil {
		return nil, ErrRetention
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 3 || !bytes.Equal(bytes.TrimSpace(fields["version"]), []byte("1")) {
		return nil, ErrRetention
	}
	for name, value := range fields {
		switch name {
		case "version":
		case "partition", "fingerprint":
			var text *string
			if json.Unmarshal(value, &text) != nil || text == nil {
				return nil, ErrRetention
			}
		default:
			return nil, ErrRetention
		}
	}
	var value Retention
	if wirejson.Strict(data, &value) != nil || value.ValidateDescriptor() != nil {
		return nil, ErrRetention
	}
	return &value, nil
}

// ParseRetention additionally validates the exact raw navigation-envelope HTML.
func ParseRetention(raw, html string) (*Retention, error) {
	value, err := ParseRetentionDescriptor(raw)
	if err != nil || value == nil {
		return value, err
	}
	if value.Validate(html) != nil {
		return nil, ErrRetention
	}
	return value, nil
}
