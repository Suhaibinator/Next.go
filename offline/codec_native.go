//go:build !js

package offline

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"unicode/utf8"
)

// MarshalCatalog is a native publication helper. The portable worker contract
// does not import this codec or encoding/json. Storage metadata/commit bytes are
// reserved separately from payload bytes; encoding must fit that allowance.
func MarshalCatalog(c *Catalog) ([]byte, error) {
	if c == nil || c.policy == nil || c.generation == "" {
		return nil, ErrInvalid
	}
	data, err := json.Marshal(c.Spec())
	if err != nil || len(data) > MaxCatalogBytes {
		return nil, ErrQuota
	}
	return data, nil
}

// ParseCatalog rejects duplicate keys at every depth, unknown fields, trailing
// values, excessive nesting, invalid values and oversized JSON before returning
// a completely normalized, owned catalog. It does not approve network content.
func ParseCatalog(data []byte) (*Catalog, error) {
	if len(data) == 0 || len(data) > MaxCatalogBytes || !utf8.Valid(data) {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueValue(d, 0, reflect.TypeOf(CatalogSpec{})); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var spec CatalogSpec
	if err := d.Decode(&spec); err != nil {
		return nil, ErrInvalid
	}
	return NewCatalog(spec)
}

func uniqueValue(d *json.Decoder, depth int, shape reflect.Type) error {
	if depth > 8 {
		return ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		if token == nil {
			return ErrInvalid
		}
		return nil
	}
	switch delimiter {
	case '{':
		if shape.Kind() != reflect.Struct {
			return ErrInvalid
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < shape.NumField(); i++ {
			f := shape.Field(i)
			fields[f.Name] = f.Type
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			field, known := fields[name]
			if err != nil || !ok || !known || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			if err := uniqueValue(d, depth+1, field); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		if shape.Kind() != reflect.Slice {
			return ErrInvalid
		}
		for d.More() {
			if err := uniqueValue(d, depth+1, shape.Elem()); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
