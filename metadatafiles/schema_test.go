//go:build !js

package metadatafiles

import (
	"bytes"
	"strings"
	"testing"
)

func TestRobotsPlainTextCompatibilityAndSafety(t *testing.T) {
	for _, value := range []string{"", "# custom\r\nUser-agent: *\r\nCrawl-delay: 3\r\nHost: example.test\r\n", "Custom-Crawler-Rule: value\n"} {
		if _, err := Validate("robots.txt", []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range [][]byte{[]byte("hello\x00secret"), {255}, bytes.Repeat([]byte("x"), 8193), bytes.Repeat([]byte("\n"), 8193)} {
		if _, err := Validate("robots.txt", value); err == nil {
			t.Fatal("invalid robots text accepted")
		}
	}
}

func TestSitemapSchemaAndNoExternalProcessing(t *testing.T) {
	valid := `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="` + sitemapNamespace + `"><url><loc>https://example.test/a?x=1&amp;y=2</loc><lastmod>2026-01-02</lastmod><priority>0.5</priority></url></urlset>`
	if _, err := Validate("sitemap.xml", []byte(valid)); err != nil {
		t.Fatal(err)
	}
	index := `<sitemapindex xmlns="` + sitemapNamespace + `"><sitemap><loc>https://example.test/sitemap-1.xml</loc></sitemap></sitemapindex>`
	if _, err := Validate("sitemap.xml", []byte(index)); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"doctype":    `<!DOCTYPE urlset SYSTEM "https://never-fetch.invalid/secret"><urlset xmlns="` + sitemapNamespace + `"/>`,
		"entity":     `<!DOCTYPE urlset [<!ENTITY secret SYSTEM "file:///etc/passwd">]><urlset xmlns="` + sitemapNamespace + `"><url><loc>&secret;</loc></url></urlset>`,
		"stylesheet": `<?xml-stylesheet href="https://never-fetch.invalid/a.xsl"?><urlset xmlns="` + sitemapNamespace + `"/>`,
		"wrong-root": `<html/>`, "namespace": `<urlset><url><loc>https://example.test/</loc></url></urlset>`,
		"unsafe-url":    strings.Replace(valid, "https://example.test/a?x=1&amp;y=2", "javascript:bad", 1),
		"credentials":   strings.Replace(valid, "https://example.test/a?x=1&amp;y=2", "https://name:password@example.test/", 1),
		"missing-loc":   `<urlset xmlns="` + sitemapNamespace + `"><url/></urlset>`,
		"duplicate-loc": `<urlset xmlns="` + sitemapNamespace + `"><url><loc>https://example.test/</loc><loc>https://example.test/</loc></url></urlset>`,
		"nested":        `<urlset xmlns="` + sitemapNamespace + `"><url><loc><b>https://example.test/</b></loc></url></urlset>`,
		"priority":      strings.Replace(valid, "0.5", "NaN", 1),
		"extra-root":    valid + `<urlset xmlns="` + sitemapNamespace + `"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate("sitemap.xml", []byte(value)); err == nil {
				t.Fatal("unsupported sitemap accepted")
			}
		})
	}
	tooMany := `<urlset xmlns="` + sitemapNamespace + `">` + strings.Repeat(`<url><loc>https://example.test/</loc></url>`, MaxSitemapEntries+1) + `</urlset>`
	if _, err := Validate("sitemap.xml", []byte(tooMany)); err == nil {
		t.Fatal("entry limit ignored")
	}
}

func TestManifestSchemaURLsAndNullAmbiguity(t *testing.T) {
	value := Manifest{Name: "Go app", ShortName: "Go", Lang: "en-US", Dir: "ltr", StartURL: "./", Scope: "./", Display: "standalone", ThemeColor: "#fff", Icons: []ManifestIcon{{Src: "icon.png", Sizes: "32x32", Type: "image/png", Purpose: "any maskable"}}}
	data, err := MarshalManifest(value, Options{BasePath: "/base"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(data, Options{BasePath: "/base"})
	if err != nil || parsed.Name != "Go app" {
		t.Fatal(parsed, err)
	}
	for name, input := range map[string]string{
		"unknown": `{"unknown":true}`, "duplicate": `{"name":"one","name":"two"}`, "case-key": `{"Name":"one"}`, "null": `null`, "null-field": `{"name":null}`, "null-icon": `{"icons":[null]}`, "null-src": `{"icons":[{"src":null}]}`, "icon-extra": `{"icons":[{"src":"icon.png","handler":"bad"}]}`,
		"remote": `{"start_url":"https://evil.test/"}`, "scheme-relative": `{"start_url":"//evil.test/"}`, "javascript": `{"start_url":"javascript:bad"}`, "outside-mount": `{"start_url":"../outside"}`, "encoded-dot": `{"start_url":"%2e%2e/outside"}`, "backslash": `{"start_url":"/%5cevil"}`, "outside-scope": `{"start_url":"./other","scope":"./private/"}`, "scope-query": `{"scope":"./?x=1"}`, "scope-file": `{"scope":"./path"}`,
		"remote-icon": `{"icons":[{"src":"https://evil.test/icon.png"}]}`, "svg-icon": `{"icons":[{"src":"icon.svg"}]}`, "wrong-type": `{"icons":[{"src":"icon.png","type":"image/svg+xml"}]}`, "bad-size": `{"icons":[{"src":"icon.png","sizes":"999999x999999"}]}`, "bad-purpose": `{"icons":[{"src":"icon.png","purpose":"script"}]}`, "duplicate-icon": `{"icons":[{"src":"icon.png"},{"src":"./icon.png"}]}`, "bad-color": `{"theme_color":"url(https://evil.test/)"}`, "bad-display": `{"display":"execute"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(input), Options{BasePath: "/base"}); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	value.Icons = make([]ManifestIcon, MaxManifestIcons+1)
	if _, err := MarshalManifest(value, Options{}); err == nil {
		t.Fatal("icon bound ignored")
	}
}
