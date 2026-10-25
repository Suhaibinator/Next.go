package zonecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

func ParseReleaseManifest(data []byte) (ReleaseManifest, error) {
	var value ReleaseManifest
	if err := parseManifest(data, &value); err != nil {
		return ReleaseManifest{}, err
	}
	return NormalizeReleaseManifest(value)
}

func ParseIngressManifest(data []byte) (IngressManifest, error) {
	var value IngressManifest
	if err := parseManifest(data, &value); err != nil {
		return IngressManifest{}, err
	}
	return NormalizeIngressManifest(value)
}

func parseManifest(data []byte, destination any) error {
	if len(data) == 0 || len(data) > MaxManifestBytes || !utf8.Valid(data) {
		return fmt.Errorf("zone contract: manifest requires valid UTF-8 JSON of at most 1 MiB")
	}
	if err := wirejson.Unique(data); err != nil {
		return fmt.Errorf("zone contract: invalid or duplicate manifest JSON: %w", err)
	}
	if err := exactShape(data, reflect.TypeOf(destination).Elem()); err != nil {
		return err
	}
	if err := wirejson.Strict(data, destination); err != nil {
		return fmt.Errorf("zone contract: invalid manifest schema: %w", err)
	}
	return nil
}

// encoding/json matches struct fields case-insensitively and accepts null for
// strings. Deployment identities instead use exact wire names and reject null,
// omitted required members, and aliases such as version/Version.
func exactShape(data json.RawMessage, target reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("zone contract: manifest members cannot be null")
	}
	switch target.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
			return fmt.Errorf("zone contract: expected manifest object")
		}
		known := make(map[string]reflect.Type, target.NumField())
		for i := 0; i < target.NumField(); i++ {
			field := target.Field(i)
			tag := field.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			known[name] = field.Type
			if _, exists := fields[name]; !exists && options != "omitempty" {
				return fmt.Errorf("zone contract: missing required manifest member %q", name)
			}
		}
		for name, value := range fields {
			kind, ok := known[name]
			if !ok {
				return fmt.Errorf("zone contract: unknown or case-aliased manifest member %q", name)
			}
			if err := exactShape(value, kind); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil || values == nil {
			return fmt.Errorf("zone contract: expected manifest array")
		}
		for _, value := range values {
			if err := exactShape(value, target.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
