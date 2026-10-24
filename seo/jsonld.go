package seo

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/internal/jsonld"
	"github.com/Suhaibinator/Next.go/ui"
	"io"
)

// StructuredData is an immutable, validated JSON-LD object or object array.
// Its serialized contents are public browser/crawler data, never secrets.
type StructuredData struct{ data []byte }

// JSONLD snapshots a JSON-serializable object or object array. HTML characters
// are escaped even when input implements json.Marshaler or is RawMessage, so a
// literal closing script tag cannot escape the inert application/ld+json node.
// The payload is limited to 256 KiB, 64 nesting levels and 64 array entities.
func JSONLD(value any) (StructuredData, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return StructuredData{}, err
	}
	if err := jsonld.Validate(data); err != nil {
		return StructuredData{}, err
	}
	return StructuredData{data: data}, nil
}

func (d StructuredData) Element() (Element, error) {
	if err := jsonld.Validate(d.data); err != nil {
		return Element{}, err
	}
	return Element{Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: string(d.data)}, nil
}

// Node renders a framework-managed, non-executable structured-data node. Use
// Page.JSONLD to keep it synchronized with metadata during client navigation.
func (d StructuredData) Node() ui.Node { return structuredDataNode{data: d} }

type structuredDataNode struct{ data StructuredData }

func (n structuredDataNode) Render(w io.Writer) error {
	if _, err := n.data.Element(); err != nil {
		return err
	}
	_, err := io.WriteString(w, `<script type="application/ld+json" data-nextgo-head="">`+string(n.data.data)+`</script>`)
	return err
}
