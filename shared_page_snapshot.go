package nextgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

// ErrSharedPageSnapshot identifies an incompatible or malformed stored page.
// Authenticated shared storage and its writers are trusted with rendered body
// HTML. This codec bounds that body; it is not an HTML sanitizer or signature.
var ErrSharedPageSnapshot = errors.New("nextgo: invalid shared page snapshot")

const (
	sharedPageFormat          = 1
	maxSharedPageBody         = 4 << 20
	maxSharedPageHeadBytes    = 1 << 20
	maxSharedPageHeadMarkup   = 4 << 20
	maxSharedPageHeadElements = 512
	maxSharedPageMetaBytes    = 256 << 10
	maxSharedPageText         = 64 << 10
)

// sharedPageSnapshot persists only materialized public values. Head markup is
// deliberately absent: SSR markup is regenerated from the same validated head
// descriptors used by navigation. Binding namespaces the expected code/config.
type sharedPageSnapshot struct {
	Format      int               `json:"format"`
	Binding     string            `json:"binding"`
	HTML        string            `json:"html"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Meta        map[string]string `json:"meta,omitempty"`
	Document    Document          `json:"document"`
	Head        []seo.Element     `json:"head"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Prerendered bool              `json:"prerendered,omitempty"`
}

func encodeSharedPage(value cachedPage, binding string) (sharedPageSnapshot, error) {
	if value.Page.document == nil || value.Page.preparedHead == nil || value.Page.Stream {
		return sharedPageSnapshot{}, sharedSnapshotError("page is not a frozen render")
	}
	dto := sharedPageSnapshot{
		Format: sharedPageFormat, Binding: binding, HTML: value.HTML,
		Title: value.Page.Title, Description: value.Page.Description, Meta: value.Page.Meta,
		Document: *value.Page.document, Head: value.Page.preparedHead.elements,
		GeneratedAt: value.GeneratedAt,
	}
	if value.Prerendered {
		dto.Format = 2
		dto.Prerendered = true
	}
	owned, _, err := validateSharedPage(dto, binding)
	return owned, err
}

func decodeSharedPage(value sharedPageSnapshot, binding string) (cachedPage, error) {
	owned, markup, err := validateSharedPage(value, binding)
	if err != nil {
		return cachedPage{}, err
	}
	return cachedPage{
		HTML: owned.HTML, GeneratedAt: owned.GeneratedAt, Prerendered: owned.Prerendered,
		Page: Page{Title: owned.Title, Description: owned.Description, Meta: owned.Meta,
			document: &owned.Document, preparedHead: &renderedHead{elements: owned.Head, markup: markup}},
	}, nil
}

func sharedSnapshotError(reason string) error {
	return fmt.Errorf("%w: %s", ErrSharedPageSnapshot, reason)
}
func snapshotText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func validateSharedPage(value sharedPageSnapshot, binding string) (sharedPageSnapshot, string, error) {
	if !snapshotText(binding, 512) || binding == "" || value.Binding != binding {
		return sharedPageSnapshot{}, "", sharedSnapshotError("binding")
	}
	if value.Format != sharedPageFormat && value.Format != 2 || value.Format == sharedPageFormat && value.Prerendered || value.Format == 2 && !value.Prerendered {
		return sharedPageSnapshot{}, "", sharedSnapshotError("format")
	}
	if value.GeneratedAt.IsZero() || value.GeneratedAt.Year() < 1 || value.GeneratedAt.Year() > 9999 {
		return sharedPageSnapshot{}, "", sharedSnapshotError("generation timestamp")
	}
	if !snapshotText(value.HTML, maxSharedPageBody) {
		return sharedPageSnapshot{}, "", sharedSnapshotError("body bounds")
	}
	if !snapshotText(value.Title, maxSharedPageText) || !snapshotText(value.Description, maxSharedPageText) || len(value.Meta) > 256 {
		return sharedPageSnapshot{}, "", sharedSnapshotError("metadata bounds")
	}
	metaBytes := len(value.Title) + len(value.Description)
	for name, text := range value.Meta {
		if name == "" || !snapshotText(name, 256) || !snapshotText(text, maxSharedPageText) {
			return sharedPageSnapshot{}, "", sharedSnapshotError("metadata value")
		}
		metaBytes += len(name) + len(text)
	}
	if metaBytes > maxSharedPageMetaBytes {
		return sharedPageSnapshot{}, "", sharedSnapshotError("metadata budget")
	}
	normalized, err := normalizePageMetadata(Page{Title: value.Title, Description: value.Description, Meta: value.Meta})
	if err != nil || !maps.Equal(normalized.Meta, value.Meta) || normalized.Description != value.Description {
		return sharedPageSnapshot{}, "", sharedSnapshotError("metadata ownership")
	}
	if err := value.Document.Validate(); err != nil {
		return sharedPageSnapshot{}, "", sharedSnapshotError("document")
	}
	head, markup, err := sharedSnapshotHead(value.Head)
	if err != nil {
		return sharedPageSnapshot{}, "", err
	}
	value.Meta = maps.Clone(value.Meta)
	value.Head = head
	value.GeneratedAt = value.GeneratedAt.UTC()
	return value, markup, nil
}

// headNode lives only during codec rendering. Its nodes never enter a stored
// snapshot or cachedPage; their output uses the ordinary UI escaping rules.
type headNode struct{ nodes []ui.Node }

func (h headNode) Render(w io.Writer) error {
	for _, node := range h.nodes {
		if err := node.Render(w); err != nil {
			return err
		}
	}
	return nil
}

func sharedSnapshotHead(elements []seo.Element) ([]seo.Element, string, error) {
	if len(elements) > maxSharedPageHeadElements {
		return nil, "", sharedSnapshotError("head count")
	}
	owned := make([]seo.Element, 0, len(elements))
	nodes := make([]ui.Node, 0, len(elements))
	bytes, jsonCount, jsonBytes := 0, 0, 0
	for _, source := range elements {
		if len(source.Attrs) > 10 || !snapshotText(source.Tag, 16) || !snapshotText(source.Text, 256<<10) {
			return nil, "", sharedSnapshotError("head element bounds")
		}
		bytes += len(source.Tag) + len(source.Text)
		for key, value := range source.Attrs {
			if !snapshotText(key, 32) || !snapshotText(value, maxSharedPageText) {
				return nil, "", sharedSnapshotError("head attribute bounds")
			}
			bytes += len(key) + len(value)
		}
		if bytes > maxSharedPageHeadBytes {
			return nil, "", sharedSnapshotError("head budget")
		}
		if source.Tag == "script" {
			jsonCount++
			jsonBytes += len(source.Text)
			if jsonCount > 16 || jsonBytes > 512<<10 || len(source.Attrs) != 1 || source.Attrs["type"] != "application/ld+json" {
				return nil, "", sharedSnapshotError("structured data ownership")
			}
			// Re-marshal the raw JSON through the existing inert-data API. Stored
			// text may contain literal </script>; fresh escaping must precede SSR.
			data, err := seo.JSONLD(json.RawMessage(source.Text))
			if err != nil {
				return nil, "", sharedSnapshotError("structured data")
			}
			element, err := data.Element()
			if err != nil {
				return nil, "", sharedSnapshotError("structured data")
			}
			owned = append(owned, element)
			nodes = append(nodes, data.Node())
			continue
		}
		if source.Text != "" {
			return nil, "", sharedSnapshotError("unexpected head text")
		}
		attrs := maps.Clone(source.Attrs)
		switch source.Tag {
		case "meta":
			if !sharedSnapshotMeta(attrs) {
				return nil, "", sharedSnapshotError("head metadata")
			}
		case "link":
			if !sharedSnapshotLink(attrs) {
				return nil, "", sharedSnapshotError("head link")
			}
		default:
			return nil, "", sharedSnapshotError("head tag")
		}
		owned = append(owned, seo.Element{Tag: source.Tag, Attrs: attrs})
		renderAttrs := ui.Attrs{"data-nextgo-head": ""}
		for key, value := range attrs {
			renderAttrs[key] = value
		}
		nodes = append(nodes, ui.El(source.Tag, renderAttrs))
	}
	markup, err := ui.HTML(headNode{nodes})
	if err != nil || len(markup) > maxSharedPageHeadMarkup {
		return nil, "", sharedSnapshotError("head markup")
	}
	return owned, markup, nil
}

func sharedSnapshotMeta(attrs map[string]string) bool {
	name, property := attrs["name"], attrs["property"]
	_, hasName := attrs["name"]
	_, hasProperty := attrs["property"]
	if hasName == hasProperty || (name == "") == (property == "") || len(name) > 256 || len(property) > 256 {
		return false
	}
	if strings.EqualFold(name, "viewport") || strings.EqualFold(name, "description") {
		return false
	}
	if _, ok := attrs["content"]; !ok {
		return false
	}
	for key := range attrs {
		if key != "name" && key != "property" && key != "content" && !(key == "media" && name == "theme-color") {
			return false
		}
	}
	return true
}

type sharedHeadResource struct{ element seo.Element }

func (r sharedHeadResource) Elements() ([]seo.Element, error) { return []seo.Element{r.element}, nil }

func sharedSnapshotLink(attrs map[string]string) bool {
	href := attrs["href"]
	if href == "" || len(href) > 32<<10 || strings.Contains(href, "\\") || strings.IndexFunc(href, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(href)
	if err != nil || u.User != nil || u.Opaque != "" {
		return false
	}
	if strings.HasPrefix(href, "/") {
		if strings.HasPrefix(href, "//") || u.Scheme != "" || u.Host != "" {
			return false
		}
	} else if seo.AbsoluteURL(href) != nil {
		return false
	}
	allowed := map[string]bool{"rel": true, "href": true}
	switch attrs["rel"] {
	case "canonical":
	case "alternate":
		allowed["hreflang"] = true
		if attrs["hreflang"] == "" || len(attrs["hreflang"]) > 255 {
			return false
		}
	case "icon", "apple-touch-icon", "mask-icon":
		allowed["type"], allowed["sizes"] = true, true
	case "stylesheet", "preload":
		// The href is already deployment-resolved and may use a configured CDN.
		// Reuse resource attribute/SRI validation with a known local placeholder,
		// after validating the actual href's safe URL shape above.
		local := maps.Clone(attrs)
		local["href"] = "/_nextgo-shared-head-validation"
		_, err := assets.Elements(sharedHeadResource{seo.Element{Tag: "link", Attrs: local}})
		return err == nil
	default:
		return false
	}
	for key := range attrs {
		if !allowed[key] {
			return false
		}
	}
	return true
}
