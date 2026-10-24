package seo

import (
	"fmt"
	"net/url"
	"strings"
)

// ResolveURL resolves a relative metadata URL against a trusted BaseURL. A base
// path is treated as a directory. Absolute HTTP(S) URLs remain independent.
// Authority-relative references and credentials are rejected.
func ResolveURL(base, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if err := AbsoluteURL(value); err == nil {
		return value, nil
	}
	if base == "" {
		return "", fmt.Errorf("seo: relative URL requires BaseURL")
	}
	if err := AbsoluteURL(base); err != nil {
		return "", fmt.Errorf("seo: invalid BaseURL: %w", err)
	}
	parent, _ := url.Parse(base)
	if parent.RawQuery != "" || parent.ForceQuery || parent.Fragment != "" || strings.Contains(base, "#") {
		return "", fmt.Errorf("seo: BaseURL must not contain a query or fragment")
	}
	if !strings.HasSuffix(parent.Path, "/") {
		parent.Path += "/"
		if parent.RawPath != "" {
			parent.RawPath += "/"
		}
	}
	child, err := url.Parse(value)
	if err != nil || child.Scheme != "" || child.Host != "" || child.User != nil || child.Opaque != "" || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return "", fmt.Errorf("seo: invalid relative metadata URL")
	}
	resolved := parent.ResolveReference(child).String()
	if err := AbsoluteURL(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func resolveMetadataURLs(m Metadata) (Metadata, error) {
	// Merge owns every map/slice before replacing URL fields.
	m = Merge(Metadata{}, m)
	if m.BaseURL != "" {
		if err := AbsoluteURL(m.BaseURL); err != nil {
			return Metadata{}, fmt.Errorf("seo: invalid BaseURL: %w", err)
		}
		base, _ := url.Parse(m.BaseURL)
		if base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Contains(m.BaseURL, "#") {
			return Metadata{}, fmt.Errorf("seo: BaseURL must not contain a query or fragment")
		}
	}
	var err error
	for _, field := range []*string{&m.Canonical, &m.OpenGraph.URL} {
		if *field, err = ResolveURL(m.BaseURL, *field); err != nil {
			return Metadata{}, err
		}
	}
	for i := range m.OpenGraph.Images {
		if m.OpenGraph.Images[i].URL, err = ResolveURL(m.BaseURL, m.OpenGraph.Images[i].URL); err != nil {
			return Metadata{}, err
		}
	}
	for i := range m.Twitter.Images {
		if m.Twitter.Images[i], err = ResolveURL(m.BaseURL, m.Twitter.Images[i]); err != nil {
			return Metadata{}, err
		}
	}
	for language, value := range m.Alternates {
		if m.Alternates[language], err = ResolveURL(m.BaseURL, value); err != nil {
			return Metadata{}, err
		}
	}
	return m, nil
}
