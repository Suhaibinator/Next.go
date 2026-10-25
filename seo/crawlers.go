package seo

import (
	"encoding/xml"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type SitemapEntry struct {
	URL             string
	LastModified    time.Time
	ChangeFrequency string
	// Priority, when set, must be between zero and one inclusive.
	Priority *float64
}

// Sitemap validates and snapshots entries into an XML HTTP handler. Applications
// with more than 50,000 URLs must split them into multiple sitemap documents.
func Sitemap(entries []SitemapEntry) (http.Handler, error) {
	data, err := MarshalSitemap(entries)
	if err != nil {
		return nil, err
	}
	return staticHandler("application/xml; charset=utf-8", data), nil
}

// MarshalSitemap validates entries and returns their escaped XML representation.
// It shares Sitemap's 50,000 URL and 50 MiB limits and does not fetch any URLs.
func MarshalSitemap(entries []SitemapEntry) ([]byte, error) {
	if len(entries) > 50000 {
		return nil, fmt.Errorf("seo: sitemap exceeds 50000 URLs")
	}
	type entry struct {
		Location        string `xml:"loc"`
		LastModified    string `xml:"lastmod,omitempty"`
		ChangeFrequency string `xml:"changefreq,omitempty"`
		Priority        string `xml:"priority,omitempty"`
	}
	doc := struct {
		XMLName   xml.Name `xml:"urlset"`
		Namespace string   `xml:"xmlns,attr"`
		Entries   []entry  `xml:"url"`
	}{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, e := range entries {
		if err := AbsoluteURL(e.URL); err != nil {
			return nil, err
		}
		v := entry{Location: e.URL, ChangeFrequency: e.ChangeFrequency}
		if !e.LastModified.IsZero() {
			if y := e.LastModified.Year(); y < 1 || y > 9999 {
				return nil, fmt.Errorf("seo: sitemap timestamp year out of range")
			}
			v.LastModified = e.LastModified.UTC().Format(time.RFC3339)
		}
		switch e.ChangeFrequency {
		case "", "always", "hourly", "daily", "weekly", "monthly", "yearly", "never":
		default:
			return nil, fmt.Errorf("seo: invalid sitemap change frequency %q", e.ChangeFrequency)
		}
		if e.Priority != nil {
			p := *e.Priority
			if math.IsNaN(p) || p < 0 || p > 1 {
				return nil, fmt.Errorf("seo: sitemap priority must be between 0 and 1")
			}
			v.Priority = strconv.FormatFloat(p, 'f', -1, 64)
		}
		doc.Entries = append(doc.Entries, v)
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append([]byte(xml.Header), data...)
	if len(data) > 50*1024*1024 {
		return nil, fmt.Errorf("seo: sitemap exceeds 50 MiB")
	}
	return data, nil
}

type SitemapIndexEntry struct {
	URL          string
	LastModified time.Time
}

// SitemapIndex links up to 50,000 separate sitemap documents.
func SitemapIndex(entries []SitemapIndexEntry) (http.Handler, error) {
	if len(entries) > 50000 {
		return nil, fmt.Errorf("seo: sitemap index exceeds 50000 entries")
	}
	type entry struct {
		Location     string `xml:"loc"`
		LastModified string `xml:"lastmod,omitempty"`
	}
	doc := struct {
		XMLName   xml.Name `xml:"sitemapindex"`
		Namespace string   `xml:"xmlns,attr"`
		Entries   []entry  `xml:"sitemap"`
	}{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, e := range entries {
		if err := AbsoluteURL(e.URL); err != nil {
			return nil, err
		}
		v := entry{Location: e.URL}
		if !e.LastModified.IsZero() {
			if y := e.LastModified.Year(); y < 1 || y > 9999 {
				return nil, fmt.Errorf("seo: sitemap timestamp year out of range")
			}
			v.LastModified = e.LastModified.UTC().Format(time.RFC3339)
		}
		doc.Entries = append(doc.Entries, v)
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append([]byte(xml.Header), data...)
	if len(data) > 50*1024*1024 {
		return nil, fmt.Errorf("seo: sitemap index exceeds 50 MiB")
	}
	return staticHandler("application/xml; charset=utf-8", data), nil
}

type RobotsRule struct {
	UserAgents      []string
	Allow, Disallow []string
}
type RobotsOptions struct {
	Rules    []RobotsRule
	Sitemaps []string
}

// Robots returns a robots.txt handler. A rule without UserAgents targets "*".
// Rules are crawler instructions; they do not enforce access control.
func Robots(options RobotsOptions) (http.Handler, error) {
	data, err := MarshalRobots(options)
	if err != nil {
		return nil, err
	}
	return staticHandler("text/plain; charset=utf-8", data), nil
}

// MarshalRobots validates options and returns crawler directives as plain text.
// It shares Robots' syntax, does not fetch sitemap URLs and is not access control.
func MarshalRobots(options RobotsOptions) ([]byte, error) {
	var b strings.Builder
	validLine := func(s string) bool {
		return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) }) < 0
	}
	for _, rule := range options.Rules {
		agents := rule.UserAgents
		if len(agents) == 0 {
			agents = []string{"*"}
		}
		for _, agent := range agents {
			if strings.TrimSpace(agent) == "" || !validLine(agent) || strings.ContainsAny(agent, "#:") {
				return nil, fmt.Errorf("seo: invalid robots user agent")
			}
			fmt.Fprintf(&b, "User-agent: %s\n", agent)
		}
		for _, set := range []struct {
			name  string
			paths []string
		}{{"Allow", rule.Allow}, {"Disallow", rule.Disallow}} {
			for _, path := range set.paths {
				if !validLine(path) || strings.Contains(path, "#") || (path != "" && !strings.HasPrefix(path, "/")) {
					return nil, fmt.Errorf("seo: invalid robots path %q", path)
				}
				fmt.Fprintf(&b, "%s: %s\n", set.name, path)
			}
		}
		b.WriteByte('\n')
	}
	for _, sitemap := range options.Sitemaps {
		if err := AbsoluteURL(sitemap); err != nil {
			return nil, err
		}
		if strings.Contains(sitemap, "#") {
			return nil, fmt.Errorf("seo: robots sitemap cannot contain a fragment")
		}
		fmt.Fprintf(&b, "Sitemap: %s\n", sitemap)
	}
	return []byte(b.String()), nil
}

func staticHandler(contentType string, data []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}
