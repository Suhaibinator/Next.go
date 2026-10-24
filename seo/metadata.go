// Package seo provides typed, escaped document metadata and crawler resources.
package seo

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Suhaibinator/Next.go/ui"
)

// Metadata describes a document head. Empty fields inherit when Merge is used.
// URLs other than icon paths require absolute HTTP(S) values unless BaseURL
// is set; BaseURL enables validated relative canonical/social/alternate URLs.
type Metadata struct {
	// BaseURL is a trusted absolute HTTP(S) directory base for canonical, social
	// and alternate URLs. Icon paths remain application asset references.
	BaseURL                               string
	TitleTemplate                         string
	AbsoluteTitle                         bool
	Language, Direction                   string
	Viewport                              *Viewport
	Title, Description, Canonical, Robots string
	OpenGraph                             OpenGraph
	Twitter                               Twitter
	Alternates                            map[string]string
	Icons                                 []Icon
}

type OpenGraph struct {
	Title, Description, URL, Type, SiteName, Locale string
	Images                                          []Image
}

type Image struct {
	URL, Alt      string
	Width, Height int
}

type Twitter struct {
	Card, Title, Description, Site, Creator string
	Images                                  []string
}

// Icon defaults to rel="icon". URL can also be a root-relative public asset.
type Icon struct{ URL, Rel, Type, Sizes string }

// Element is structured head data, never raw HTML. Attrs and Text must be
// escaped by consumers. Only title, meta and link tags are produced.
type Element struct {
	Tag   string            `json:"tag"`
	Attrs map[string]string `json:"attrs,omitempty"`
	Text  string            `json:"text,omitempty"`
}

// AbsoluteURL rejects non-web URLs, credentials, whitespace and control bytes.
func AbsoluteURL(value string) error {
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("seo: invalid absolute URL %q", value)
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return fmt.Errorf("seo: expected absolute HTTP(S) URL, got %q", value)
	}
	return nil
}

// Elements validates metadata and returns a deterministic structured head.
func Elements(m Metadata) ([]Element, error) {
	var err error
	m, err = resolveMetadataURLs(m)
	if err != nil {
		return nil, err
	}
	if err := ValidateTitleTemplate(m.TitleTemplate); err != nil {
		return nil, err
	}
	document, err := ResolveDocument(m)
	if err != nil {
		return nil, err
	}
	var out []Element
	meta := func(key, value, attribute string) {
		if value != "" {
			out = append(out, Element{Tag: "meta", Attrs: map[string]string{attribute: key, "content": value}})
		}
	}
	link := func(rel, href string, attrs map[string]string) {
		if attrs == nil {
			attrs = map[string]string{}
		}
		attrs["rel"] = rel
		attrs["href"] = href
		out = append(out, Element{Tag: "link", Attrs: attrs})
	}
	if m.Title != "" {
		out = append(out, Element{Tag: "title", Text: m.Title})
	}
	meta("description", m.Description, "name")
	meta("robots", m.Robots, "name")
	if m.Viewport != nil {
		meta("viewport", document.Viewport, "name")
		meta("color-scheme", m.Viewport.ColorScheme, "name")
		for _, color := range m.Viewport.ThemeColors {
			attrs := map[string]string{"name": "theme-color", "content": color.Color}
			if color.Media != "" {
				attrs["media"] = color.Media
			}
			out = append(out, Element{Tag: "meta", Attrs: attrs})
		}
	}
	if m.Canonical != "" {
		if err := AbsoluteURL(m.Canonical); err != nil {
			return nil, err
		}
		link("canonical", m.Canonical, nil)
	}
	og := m.OpenGraph
	for _, kv := range [][2]string{{"title", og.Title}, {"description", og.Description}, {"type", og.Type}, {"site_name", og.SiteName}, {"locale", og.Locale}} {
		meta("og:"+kv[0], kv[1], "property")
	}
	if og.URL != "" {
		if err := AbsoluteURL(og.URL); err != nil {
			return nil, err
		}
		meta("og:url", og.URL, "property")
	}
	for _, img := range og.Images {
		if err := AbsoluteURL(img.URL); err != nil {
			return nil, err
		}
		if img.Width < 0 || img.Height < 0 {
			return nil, fmt.Errorf("seo: image dimensions must be nonnegative")
		}
		meta("og:image", img.URL, "property")
		meta("og:image:alt", img.Alt, "property")
		if img.Width > 0 {
			meta("og:image:width", strconv.Itoa(img.Width), "property")
		}
		if img.Height > 0 {
			meta("og:image:height", strconv.Itoa(img.Height), "property")
		}
	}
	t := m.Twitter
	for _, kv := range [][2]string{{"card", t.Card}, {"title", t.Title}, {"description", t.Description}, {"site", t.Site}, {"creator", t.Creator}} {
		meta("twitter:"+kv[0], kv[1], "name")
	}
	for _, img := range t.Images {
		if err := AbsoluteURL(img); err != nil {
			return nil, err
		}
		meta("twitter:image", img, "name")
	}
	keys := make([]string, 0, len(m.Alternates))
	for lang := range m.Alternates {
		keys = append(keys, lang)
	}
	sort.Strings(keys)
	for _, lang := range keys {
		href := m.Alternates[lang]
		if err := AbsoluteURL(href); err != nil {
			return nil, err
		}
		if lang == "" {
			return nil, fmt.Errorf("seo: alternate language is empty")
		}
		link("alternate", href, map[string]string{"hreflang": lang})
	}
	for _, icon := range m.Icons {
		if !(strings.HasPrefix(icon.URL, "/") && !strings.HasPrefix(icon.URL, "//")) {
			if err := AbsoluteURL(icon.URL); err != nil {
				return nil, err
			}
		} else if strings.ContainsAny(icon.URL, "\\\r\n\t") {
			return nil, fmt.Errorf("seo: invalid icon URL")
		}
		rel := icon.Rel
		if rel == "" {
			rel = "icon"
		}
		attrs := map[string]string{}
		if icon.Type != "" {
			attrs["type"] = icon.Type
		}
		if icon.Sizes != "" {
			attrs["sizes"] = icon.Sizes
		}
		link(rel, icon.URL, attrs)
	}
	return out, nil
}

// Node returns a safely rendered head fragment. Invalid metadata fails rendering.
func Node(m Metadata) ui.Node { return metadataNode{m} }

type metadataNode struct{ metadata Metadata }

func (n metadataNode) Render(w io.Writer) error {
	elements, err := Elements(n.metadata)
	if err != nil {
		return err
	}
	for _, e := range elements {
		var children []ui.Node
		if e.Tag == "title" {
			children = []ui.Node{ui.Text(e.Text)}
		}
		if err := ui.El(e.Tag, ui.Attrs(e.Attrs), children...).Render(w); err != nil {
			return err
		}
	}
	return nil
}

// Merge inherits empty scalar fields, merges alternate languages, and replaces
// image/icon lists when the child's slice is non-nil (an empty slice clears it).
// The returned maps and slices do not alias either input.
func Merge(parent, child Metadata) Metadata {
	m := child
	inherit := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	inherit(&m.TitleTemplate, parent.TitleTemplate)
	inherit(&m.Language, parent.Language)
	inherit(&m.Direction, parent.Direction)
	viewport := child.Viewport
	if viewport == nil {
		viewport = parent.Viewport
	}
	if viewport != nil {
		copy := *viewport
		copy.ThemeColors = append([]ThemeColor(nil), viewport.ThemeColors...)
		if viewport.UserScalable != nil {
			zoom := *viewport.UserScalable
			copy.UserScalable = &zoom
		}
		m.Viewport = &copy
	}
	inherit(&m.BaseURL, parent.BaseURL)
	inherit(&m.Title, parent.Title)
	inherit(&m.Description, parent.Description)
	inherit(&m.Canonical, parent.Canonical)
	inherit(&m.Robots, parent.Robots)
	p, o := parent.OpenGraph, &m.OpenGraph
	inherit(&o.Title, p.Title)
	inherit(&o.Description, p.Description)
	inherit(&o.URL, p.URL)
	inherit(&o.Type, p.Type)
	inherit(&o.SiteName, p.SiteName)
	inherit(&o.Locale, p.Locale)
	images := o.Images
	if images == nil {
		images = p.Images
	}
	if images != nil {
		o.Images = append([]Image{}, images...)
	}
	pt, t := parent.Twitter, &m.Twitter
	inherit(&t.Card, pt.Card)
	inherit(&t.Title, pt.Title)
	inherit(&t.Description, pt.Description)
	inherit(&t.Site, pt.Site)
	inherit(&t.Creator, pt.Creator)
	timages := t.Images
	if timages == nil {
		timages = pt.Images
	}
	if timages != nil {
		t.Images = append([]string{}, timages...)
	}
	icons := m.Icons
	if icons == nil {
		icons = parent.Icons
	}
	if icons != nil {
		m.Icons = append([]Icon{}, icons...)
	}
	if parent.Alternates != nil || child.Alternates != nil {
		m.Alternates = map[string]string{}
		for k, v := range parent.Alternates {
			m.Alternates[k] = v
		}
		for k, v := range child.Alternates {
			m.Alternates[k] = v
		}
	}
	return m
}
