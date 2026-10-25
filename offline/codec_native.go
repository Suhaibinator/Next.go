//go:build !js

package offline

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/internal/offlinejson"
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
	if len(data) == 0 || len(data) > MaxCatalogBytes {
		return nil, ErrInvalid
	}
	var spec CatalogSpec
	if err := offlinejson.Decode(data, &spec); err != nil {
		return nil, ErrInvalid
	}
	return NewCatalog(spec)
}
