//go:build !js

package nextgo

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
)

// Reject unsupported references in framework-rendered bytes against the
// compiler inventory. XML tokens are not a browser HTML parser: raw-text style
// and script contents are checked against their actual source bytes separately.
// This neither rewrites HTML nor sanitizes arbitrary untrusted application HTML.
func (a *App) validateManagedOfflineHTML(body []byte) error {
	state := a.managedOffline
	lower := bytes.ToLower(body)
	// HTML treats CDATA outside foreign content differently from XML.
	// This bounded HTML-only subset permits no CDATA spelling anywhere.
	if bytes.Contains(lower, []byte("<![cdata[")) {
		return ErrOfflineAdmission
	}
	if bytes.Count(lower, []byte("<style")) != 1 || bytes.Count(lower, []byte("</style")) != 1 || !bytes.Contains(body, []byte("<style>"+frameworkCSS+"</style>")) {
		return ErrOfflineAdmission
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = false
	decoder.AutoClose = append(append([]string(nil), xml.HTMLAutoClose...), "source", "track", "wbr")
	decoder.Entity = xml.HTMLEntity
	bodyCount, tokens, styleCount := 0, 0, 0
	insideStyle, insideScript, jsonScript := false, false, false
	var scriptStart int64
	for {
		tokenStart := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrOfflineAdmission
		}
		tokens++
		if tokens > 262144 {
			return ErrOfflineAdmission
		}
		if insideStyle || insideScript {
			switch current := token.(type) {
			case xml.CharData:
				continue
			case xml.EndElement:
				if insideStyle && strings.EqualFold(current.Name.Local, "style") {
					insideStyle = false
					continue
				}
				if !insideScript || !strings.EqualFold(current.Name.Local, "script") {
					return ErrOfflineAdmission
				}
				raw := body[scriptStart:tokenStart]
				if bytes.ContainsRune(raw, '<') || jsonScript && !json.Valid(raw) || !jsonScript && len(bytes.TrimSpace(raw)) != 0 {
					return ErrOfflineAdmission
				}
				insideScript = false
				continue
			default:
				return ErrOfflineAdmission
			}
		}
		switch current := token.(type) {
		case xml.Comment, xml.ProcInst:
			return ErrOfflineAdmission
		case xml.Directive:
			if !strings.EqualFold(strings.TrimSpace(string(current)), "doctype html") {
				return ErrOfflineAdmission
			}
			continue
		}
		element, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		tag := strings.ToLower(element.Name.Local)
		attrs := map[string]string{}
		for _, attribute := range element.Attr {
			name := strings.ToLower(attribute.Name.Local)
			if _, duplicate := attrs[name]; duplicate {
				return ErrOfflineAdmission
			}
			if name == "style" || name == "srcdoc" || name == "manifest" || strings.HasPrefix(name, "on") {
				return ErrOfflineAdmission
			}
			attrs[name] = attribute.Value
		}
		if tag == "body" {
			bodyCount++
			if attrs["data-nextgo-offline-saved"] != "1" || attrs["data-nextgo-offline"] != a.offlineDescriptorJSON() || attrs["data-nextgo-build"] != a.config.BuildID || attrs["data-nextgo-configuration"] != a.configurationID {
				return ErrOfflineAdmission
			}
		}
		if tag == "nextgo-script" || tag == "iframe" || tag == "object" || tag == "embed" || tag == "base" || tag == "applet" || tag == "frame" || tag == "frameset" || tag == "svg" || tag == "math" || tag == "plaintext" || tag == "xmp" || tag == "noembed" || tag == "noframes" {
			return ErrOfflineAdmission
		}
		if tag == "style" {
			styleCount++
			if styleCount != 1 || len(attrs) != 0 {
				return ErrOfflineAdmission
			}
			insideStyle = true
		}
		if tag == "meta" {
			if _, present := attrs["http-equiv"]; present {
				return ErrOfflineAdmission
			}
		}
		if tag == "script" {
			jsonScript = attrs["src"] == "" && attrs["type"] == "application/ld+json"
			if attrs["src"] == "" && !jsonScript {
				return ErrOfflineAdmission
			}
			insideScript, scriptStart = true, decoder.InputOffset()
		}
		for _, name := range []string{"src", "poster", "background"} {
			if value := attrs[name]; value != "" && !state.hasResource(value) {
				return ErrOfflineAdmission
			}
		}
		for _, name := range []string{"srcset", "imagesrcset"} {
			if value := attrs[name]; value != "" {
				for _, item := range strings.Split(value, ",") {
					parts := strings.Fields(item)
					if len(parts) != 2 || !state.hasResource(parts[0]) {
						return ErrOfflineAdmission
					}
				}
			}
		}
		if tag != "a" && tag != "area" && tag != "link" {
			if href := attrs["href"]; href != "" && !state.hasResource(href) {
				return ErrOfflineAdmission
			}
		}
		if tag == "link" {
			for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
				switch rel {
				case "stylesheet", "preload", "modulepreload", "icon", "apple-touch-icon", "manifest":
					if !state.hasResource(attrs["href"]) {
						return ErrOfflineAdmission
					}
				case "canonical", "alternate", "author", "license", "help", "next", "prev":
				default:
					return ErrOfflineAdmission
				}
			}
		}
	}
	if bodyCount != 1 || styleCount != 1 || insideStyle || insideScript {
		return ErrOfflineAdmission
	}
	return nil
}
func (s *managedOffline) hasResource(value string) bool {
	config := s.policy.Config()
	if strings.HasPrefix(value, config.Origin+"/") {
		value = strings.TrimPrefix(value, config.Origin)
	}
	_, ok := s.files[value]
	return ok
}
