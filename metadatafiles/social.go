//go:build !js

package metadatafiles

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/seo"
)

var socialImageExtensions = [...]string{".png", ".jpg", ".jpeg", ".gif"}

// CheckSocialNames validates a complete fixed-name declaration, including PNG
// names supplied by Go producers. Multiple variants for one role are ambiguous
// and can be rejected before any producer or decoder runs. Other filenames are ignored;
// callers still validate their general inventory separately.
func CheckSocialNames(names []string) error {
	for _, prefix := range []string{"opengraph-image", "twitter-image"} {
		count, alt := 0, false
		for _, name := range names {
			if name == prefix+".alt.txt" {
				alt = true
			}
			for _, ext := range socialImageExtensions {
				if name == prefix+ext {
					count++
				}
			}
		}
		if count > 1 {
			return fmt.Errorf("metadatafiles: conflicting image variants for %s", prefix)
		}
		if alt && count == 0 {
			return fmt.Errorf("metadatafiles: %s alt text requires an image or Go producer", prefix)
		}
	}
	return nil
}

func validateSocialAlt(data []byte) error {
	if len(data) == 0 || len(data) > MaxAltBytes || !utf8.Valid(data) {
		return fmt.Errorf("social image alt text exceeds UTF-8/byte limits")
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("social image alt text requires one nonempty text line")
	}
	return nil
}

// SocialHead returns owned global image defaults resolved against an explicitly
// trusted metadata base. An empty base omits automatic tags; files remain public.
// No request authority, locale path or CDN origin is inferred here. Callers
// decide whether explicit metadata overrides/clears the corresponding group.
func (s *Set) SocialHead(base string) ([]seo.Element, error) {
	if s == nil || base == "" {
		return nil, nil
	}
	var head []seo.Element
	for _, prefix := range []string{"opengraph-image", "twitter-image"} {
		var chosen resource
		ok := false
		for _, ext := range socialImageExtensions {
			if found, exists := s.byName[prefix+ext]; exists {
				chosen, ok = found, true
				break
			}
		}
		if !ok {
			continue
		}
		url, err := seo.ResolveURL(base, chosen.entry.Path)
		if err != nil {
			return nil, err
		}
		attribute, key := "property", "og:image"
		if prefix == "twitter-image" {
			attribute, key = "name", "twitter:image"
		}
		add := func(name, value string) {
			head = append(head, seo.Element{Tag: "meta", Attrs: map[string]string{attribute: name, "content": value}})
		}
		add(key, url)
		if alt, exists := s.byName[prefix+".alt.txt"]; exists {
			add(key+":alt", strings.TrimSpace(string(alt.data)))
		}
		if prefix == "opengraph-image" {
			add("og:image:type", chosen.entry.ContentType)
			add("og:image:width", strconv.Itoa(chosen.entry.Width))
			add("og:image:height", strconv.Itoa(chosen.entry.Height))
		} else {
			add("twitter:card", "summary_large_image")
		}
	}
	return head, nil
}
