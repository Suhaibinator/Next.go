package navigation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const MetadataHeader = "X-Nextgo-Metadata"
const MetadataVersion = 1
const MaxMetadataBytes = 256 << 10
const MaxMetadataElements = 256
const MaxMetadataStreamFrames = MaxStreamFrames + 1

// MetadataElement is structured SEO data, never HTML. It deliberately excludes
// resource and structured-data ownership; provenance is supplied separately.
type MetadataElement struct {
	Tag   string            `json:"tag"`
	Attrs map[string]string `json:"attrs,omitempty"`
	Text  string            `json:"text,omitempty"`
}

// MetadataPayload is encoded with EncodeMetadataPayload so failed completions
// omit replacement fields and successful empty values remain explicit.
type MetadataPayload struct {
	Version                   int
	Owner, Title, Description string
	Elements                  []MetadataElement
}

func ValidMetadataOwner(owner string) bool { return ascii.LowerHex(owner, 32) }

// MetadataAcknowledged rejects ambiguous and unsupported response negotiation.
func MetadataAcknowledged(values []string) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	if len(values) != 1 || values[0] != "1" {
		return false, fmt.Errorf("invalid metadata acknowledgment")
	}
	return true, nil
}

func EncodeMetadataPayload(p MetadataPayload, failed bool) ([]byte, error) {
	var data []byte
	var err error
	if failed {
		data, err = json.Marshal(struct {
			Version int    `json:"version"`
			Owner   string `json:"owner"`
		}{p.Version, p.Owner})
	} else {
		if p.Elements == nil {
			p.Elements = []MetadataElement{}
		}
		data, err = json.Marshal(struct {
			Version     int               `json:"version"`
			Owner       string            `json:"owner"`
			Title       string            `json:"title"`
			Description string            `json:"description"`
			Elements    []MetadataElement `json:"elements"`
		}{p.Version, p.Owner, p.Title, p.Description, p.Elements})
	}
	if err != nil {
		return nil, err
	}
	if _, err = DecodeMetadataPayload(data, failed); err != nil {
		return nil, err
	}
	return data, nil
}

func metadataString(raw json.RawMessage) (string, error) {
	var value *string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return "", fmt.Errorf("invalid metadata string")
	}
	return *value, nil
}

func DecodeMetadataPayload(data []byte, failed bool) (MetadataPayload, error) {
	var p MetadataPayload
	if len(data) == 0 || len(data) > MaxMetadataBytes || !utf8.Valid(data) || wirejson.Unique(data) != nil {
		return p, fmt.Errorf("invalid metadata payload")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return p, fmt.Errorf("invalid metadata object")
	}
	expected := 2
	if !failed {
		expected = 5
	}
	if len(fields) != expected {
		return p, fmt.Errorf("invalid metadata fields")
	}
	for k := range fields {
		if k != "version" && k != "owner" && (failed || k != "title" && k != "description" && k != "elements") {
			return p, fmt.Errorf("unknown metadata field")
		}
	}
	var version *int
	if json.Unmarshal(fields["version"], &version) != nil || version == nil || *version != MetadataVersion {
		return p, fmt.Errorf("unsupported metadata version")
	}
	p.Version = *version
	var err error
	if p.Owner, err = metadataString(fields["owner"]); err != nil || !ValidMetadataOwner(p.Owner) {
		return p, fmt.Errorf("invalid metadata owner")
	}
	if failed {
		return p, nil
	}
	if p.Title, err = metadataString(fields["title"]); err != nil {
		return p, err
	}
	if p.Description, err = metadataString(fields["description"]); err != nil {
		return p, err
	}
	raw := bytes.TrimSpace(fields["elements"])
	if len(raw) == 0 || raw[0] != '[' {
		return p, fmt.Errorf("invalid metadata elements")
	}
	var elements []json.RawMessage
	if json.Unmarshal(raw, &elements) != nil || len(elements) > MaxMetadataElements {
		return p, fmt.Errorf("invalid metadata elements")
	}
	p.Elements = make([]MetadataElement, 0, len(elements))
	for _, raw := range elements {
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) != nil || keys == nil {
			return p, fmt.Errorf("invalid metadata element")
		}
		for k := range keys {
			if k != "tag" && k != "attrs" && k != "text" {
				return p, fmt.Errorf("unknown metadata element field")
			}
		}
		var element MetadataElement
		if element.Tag, err = metadataString(keys["tag"]); err != nil {
			return p, err
		}
		if text, ok := keys["text"]; ok {
			if element.Text, err = metadataString(text); err != nil {
				return p, err
			}
		}
		attrs := bytes.TrimSpace(keys["attrs"])
		if len(attrs) == 0 || attrs[0] != '{' || json.Unmarshal(attrs, &element.Attrs) != nil || element.Attrs == nil {
			return p, fmt.Errorf("invalid metadata attributes")
		}
		// Unmarshal into strings alone accepts JSON null. Require actual strings.
		var values map[string]json.RawMessage
		if json.Unmarshal(attrs, &values) != nil {
			return p, fmt.Errorf("invalid metadata attributes")
		}
		for _, value := range values {
			if _, err = metadataString(value); err != nil {
				return p, err
			}
		}
		p.Elements = append(p.Elements, element)
	}
	if err = ValidateMetadataElements(p.Elements); err != nil {
		return p, err
	}
	return p, nil
}

func ValidateMetadataIndices(indices []int, headCount int) error {
	if len(indices) > MaxMetadataElements {
		return fmt.Errorf("too many metadata indices")
	}
	previous := -1
	for _, index := range indices {
		if index <= previous || index < 0 || index >= headCount {
			return fmt.Errorf("invalid metadata indices")
		}
		previous = index
	}
	return nil
}

// ValidateMetadataElements is shared by native encoding and the browser. The
// ordinary head validator is still applied before constructing detached nodes.
func ValidateMetadataElements(elements []MetadataElement) error {
	if len(elements) > MaxMetadataElements {
		return fmt.Errorf("too many metadata elements")
	}
	for _, e := range elements {
		if e.Text != "" {
			return fmt.Errorf("unexpected metadata text")
		}
		switch e.Tag {
		case "meta":
			name, property := e.Attrs["name"], e.Attrs["property"]
			if (name == "") == (property == "") || strings.EqualFold(name, "viewport") || strings.EqualFold(name, "description") {
				return fmt.Errorf("invalid metadata name")
			}
			for key := range e.Attrs {
				if key != "name" && key != "property" && key != "content" && !(key == "media" && name == "theme-color") {
					return fmt.Errorf("invalid metadata attribute")
				}
			}
			if _, ok := e.Attrs["content"]; !ok {
				return fmt.Errorf("missing metadata content")
			}
		case "link":
			switch e.Attrs["rel"] {
			case "canonical", "alternate", "icon", "apple-touch-icon", "mask-icon", "manifest":
			default:
				return fmt.Errorf("invalid metadata relation")
			}
			for key := range e.Attrs {
				if key != "rel" && key != "href" && key != "hreflang" && key != "type" && key != "sizes" && key != "media" {
					return fmt.Errorf("invalid metadata link attribute")
				}
			}
			href := e.Attrs["href"]
			if href == "" || strings.HasPrefix(href, "//") || strings.Contains(href, "\\") || strings.IndexFunc(href, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				return fmt.Errorf("invalid metadata URL")
			}
			u, err := url.Parse(href)
			if err != nil || u.User != nil || u.Opaque != "" || !(strings.HasPrefix(href, "/") && u.Scheme == "" && u.Host == "" || (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "") {
				return fmt.Errorf("invalid metadata URL")
			}
			if e.Attrs["rel"] == "manifest" {
				for key, value := range e.Attrs {
					if key != "rel" && key != "href" && !(key == "type" && value == "application/manifest+json") {
						return fmt.Errorf("invalid metadata manifest attribute")
					}
				}
			}
		default:
			return fmt.Errorf("invalid metadata tag")
		}
	}
	return nil
}
