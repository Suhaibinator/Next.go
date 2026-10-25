//go:build !js

package metadatafiles

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/seo"
)

// Robots is public plain text, not a closed grammar: extension directives and
// comments are preserved. Bound lines/bytes and reject binary/HTML payloads;
// this does not assert every crawler understands an application's directives.
func validateRobots(data []byte) error {
	if len(data) > MaxRobotsBytes || !utf8.Valid(data) {
		return fmt.Errorf("robots text exceeds size or UTF-8 limit")
	}
	for _, b := range data {
		if b < 32 && b != '\t' && b != '\r' && b != '\n' || b == 127 {
			return fmt.Errorf("robots text contains control bytes")
		}
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 8192 {
		return fmt.Errorf("robots text exceeds line limit")
	}
	for _, line := range lines {
		if len(line) > 8192 {
			return fmt.Errorf("robots line exceeds byte limit")
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && trimmed[0] == '<' {
			return fmt.Errorf("robots text cannot contain markup")
		}
	}
	return nil
}

// Bound typed Go inputs before invoking the shared serializer. The final raw
// representation has its own limit too; startup serialization cannot grow from
// an unbounded number of caller-owned directives or arbitrarily long strings.
func robotInputBounds(value seo.RobotsOptions) error {
	if len(value.Rules) > 8192 || len(value.Sitemaps) > 8192 {
		return fmt.Errorf("robots producer exceeds directive count")
	}
	total, count := 0, 0
	add := func(values []string) error {
		if len(values) > 8192-count {
			return fmt.Errorf("robots producer exceeds directive count")
		}
		count += len(values)
		for _, text := range values {
			if len(text) > 8192 || len(text) > MaxRobotsBytes-total {
				return fmt.Errorf("robots producer exceeds text byte limits")
			}
			total += len(text)
		}
		return nil
	}
	for _, rule := range value.Rules {
		if err := add(rule.UserAgents); err != nil {
			return err
		}
		if err := add(rule.Allow); err != nil {
			return err
		}
		if err := add(rule.Disallow); err != nil {
			return err
		}
	}
	return add(value.Sitemaps)
}

const sitemapNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

func validateSitemap(data []byte) error {
	if len(data) == 0 || len(data) > MaxSitemapBytes || !utf8.Valid(data) {
		return fmt.Errorf("sitemap exceeds size or UTF-8 limit")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var stack []xml.Name
	root, entryName := "", ""
	entries := 0
	fields := map[string]bool{}
	field, text := "", ""
	seenRoot, closed := false, false
	tokens := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid sitemap XML")
		}
		tokens++
		if tokens > MaxSitemapEntries*32+32 {
			return fmt.Errorf("sitemap exceeds token limit")
		}
		switch value := token.(type) {
		case xml.Directive:
			return fmt.Errorf("sitemap directives and entities are not supported")
		case xml.ProcInst:
			if value.Target != "xml" || seenRoot {
				return fmt.Errorf("sitemap processing instructions are not supported")
			}
		case xml.Comment: // Inert XML comments are bounded by the source limit.
		case xml.StartElement:
			if closed {
				return fmt.Errorf("sitemap has multiple roots")
			}
			depth := len(stack)
			if depth == 0 {
				if seenRoot || (value.Name.Local != "urlset" && value.Name.Local != "sitemapindex") || value.Name.Space != sitemapNamespace {
					return fmt.Errorf("unsupported sitemap root or namespace")
				}
				root = value.Name.Local
				entryName = "url"
				if root == "sitemapindex" {
					entryName = "sitemap"
				}
				seenRoot = true
				for _, attr := range value.Attr {
					if !(attr.Name.Space == "" && attr.Name.Local == "xmlns" && attr.Value == sitemapNamespace) {
						return fmt.Errorf("unsupported sitemap root attribute")
					}
				}
			} else {
				if value.Name.Space != sitemapNamespace || len(value.Attr) != 0 {
					return fmt.Errorf("invalid sitemap element namespace or attributes")
				}
				switch depth {
				case 1:
					if value.Name.Local != entryName {
						return fmt.Errorf("unsupported sitemap entry")
					}
					entries++
					if entries > MaxSitemapEntries {
						return fmt.Errorf("sitemap exceeds entry limit")
					}
					fields = map[string]bool{}
				case 2:
					field = value.Name.Local
					if field != "loc" && field != "lastmod" && (root != "urlset" || (field != "changefreq" && field != "priority")) {
						return fmt.Errorf("unsupported sitemap field")
					}
					if fields[field] {
						return fmt.Errorf("duplicate sitemap field")
					}
					fields[field] = true
					text = ""
				default:
					return fmt.Errorf("nested sitemap field is unsupported")
				}
			}
			stack = append(stack, value.Name)
		case xml.CharData:
			if len(stack) == 3 {
				if len(value) > 8192-len(text) {
					return fmt.Errorf("sitemap field exceeds byte limit")
				}
				text += string(value)
			} else if strings.TrimSpace(string(value)) != "" {
				return fmt.Errorf("unexpected sitemap text")
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != value.Name {
				return fmt.Errorf("invalid sitemap nesting")
			}
			if len(stack) == 3 {
				if err := sitemapField(field, strings.TrimSpace(text)); err != nil {
					return err
				}
			}
			if len(stack) == 2 && !fields["loc"] {
				return fmt.Errorf("sitemap entry requires loc")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				closed = true
			}
		}
	}
	if !seenRoot || !closed || len(stack) != 0 {
		return fmt.Errorf("incomplete sitemap document")
	}
	return nil
}

func sitemapField(name, value string) error {
	switch name {
	case "loc":
		if value == "" || seo.AbsoluteURL(value) != nil || strings.Contains(value, "#") {
			return fmt.Errorf("sitemap loc requires an absolute HTTP(S) URL without fragment")
		}
	case "lastmod":
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return fmt.Errorf("invalid sitemap timestamp")
			}
		}
	case "changefreq":
		switch value {
		case "always", "hourly", "daily", "weekly", "monthly", "yearly", "never":
		default:
			return fmt.Errorf("invalid sitemap change frequency")
		}
	case "priority":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 1 {
			return fmt.Errorf("invalid sitemap priority")
		}
	}
	return nil
}
