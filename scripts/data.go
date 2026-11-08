package scripts

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

// DataAttributes is an immutable, comparable set of trusted vendor data-*
// settings. Construct it with NewDataAttributes. The zero value is empty.
// A direct conversion is revalidated by Check and JSON serialization.
type DataAttributes string

// NewDataAttributes snapshots at most 16 lowercase data-* attributes. Framework
// data-nextgo* names are reserved. Values must be valid UTF-8 without controls;
// each is at most 1024 bytes and canonical encoded JSON is at most 4096 bytes.
func NewDataAttributes(values map[string]string) (DataAttributes, error) {
	if len(values) > 16 {
		return "", fmt.Errorf("scripts: at most 16 data attributes")
	}
	for name, value := range values {
		if len(name) < 6 || len(name) > 64 || !strings.HasPrefix(name, "data-") || strings.HasPrefix(name, "data-nextgo") {
			return "", fmt.Errorf("scripts: invalid or reserved data attribute name")
		}
		for i, b := range []byte(name[5:]) {
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' && i > 0) {
				return "", fmt.Errorf("scripts: data attribute names must use lowercase ASCII")
			}
		}
		if len(value) > 1024 || !utf8.ValidString(value) {
			return "", fmt.Errorf("scripts: data attribute value exceeds bounds or has invalid UTF-8")
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return "", fmt.Errorf("scripts: data attribute value contains a control character")
			}
		}
	}
	if len(values) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > 4096 {
		return "", fmt.Errorf("scripts: encoded data attributes exceed 4 KiB")
	}
	return DataAttributes(raw), nil
}

func (d DataAttributes) Check() (DataAttributes, error) {
	if d == "" {
		return "", nil
	}
	if len(d) > 4096 {
		return "", fmt.Errorf("scripts: encoded data attributes exceed 4 KiB")
	}
	if err := wirejson.Unique([]byte(d)); err != nil {
		return "", fmt.Errorf("scripts: invalid data attributes: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := wirejson.Strict([]byte(d), &fields); err != nil || fields == nil {
		return "", fmt.Errorf("scripts: data attributes require a string-valued object")
	}
	if len(fields) > 16 {
		return "", fmt.Errorf("scripts: at most 16 data attributes")
	}
	values := make(map[string]string, len(fields))
	for name, raw := range fields {
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
			return "", fmt.Errorf("scripts: data attributes require string values")
		}
		values[name] = value
	}
	return NewDataAttributes(values)
}

// Values returns a caller-owned copy. Mutating it cannot change the descriptor.
func (d DataAttributes) Values() (map[string]string, error) {
	checked, err := d.Check()
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if checked != "" {
		err = json.Unmarshal([]byte(checked), &values)
	}
	return values, err
}

func (d DataAttributes) MarshalJSON() ([]byte, error) {
	checked, err := d.Check()
	if err != nil {
		return nil, err
	}
	if checked == "" {
		return []byte(`{}`), nil
	}
	return []byte(checked), nil
}

func (d *DataAttributes) UnmarshalJSON(raw []byte) error {
	checked, err := DataAttributes(raw).Check()
	if err == nil {
		*d = checked
	}
	return err
}
