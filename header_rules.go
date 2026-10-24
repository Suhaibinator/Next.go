package nextgo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

// HeaderValue sets one response field. Append preserves preceding values;
// otherwise it replaces them. {{name}} references a path or positive capture.
type HeaderValue struct {
	Name, Value string
	Append      bool
}

// HeaderRule selects the original canonical logical URL, before middleware,
// redirects and rewrites. Rules run in order; handlers can override defaults.
type HeaderRule struct {
	Source       string
	Headers      []HeaderValue
	Has, Missing []RuleCondition
}
type headerPart struct{ literal, name string }
type compiledHeaderValue struct {
	name   string
	append bool
	parts  []headerPart
}
type compiledHeaderRule struct {
	matchRule compiledRule
	values    []compiledHeaderValue
}

func allowedRuleResponseHeader(name string) bool {
	if len(name) > 128 || !validRuleHeader(name) {
		return false
	}
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "x-nextgo-") {
		return false
	}
	switch name {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "content-length", "content-encoding", "content-type", "host", "set-cookie", "set-cookie2", "location":
		return false
	}
	return true
}
func validRuleHeaderValue(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if c < 32 && c != '\t' || c == 127 {
			return false
		}
	}
	return true
}
func compileHeaderValue(value string, names map[string]bool) ([]headerPart, error) {
	if len(value) > 8192 || !validRuleHeaderValue(value) {
		return nil, fmt.Errorf("invalid or oversized header value")
	}
	var parts []headerPart
	for len(value) > 0 {
		start := strings.Index(value, "{{")
		if start < 0 {
			parts = append(parts, headerPart{literal: value})
			break
		}
		if start > 0 {
			parts = append(parts, headerPart{literal: value[:start]})
		}
		value = value[start+2:]
		end := strings.Index(value, "}}")
		if end < 0 || !ruleIdentifier.MatchString(value[:end]) || !names[value[:end]] {
			return nil, fmt.Errorf("unknown or invalid header substitution")
		}
		parts = append(parts, headerPart{name: value[:end]})
		value = value[end+2:]
		if len(parts) > 128 {
			return nil, fmt.Errorf("header substitution exceeds 128 parts")
		}
	}
	if len(parts) > 128 {
		return nil, fmt.Errorf("header substitution exceeds 128 parts")
	}
	return parts, nil
}
func (a *App) initHeaderRules() error {
	total := 0
	owned := make([]HeaderRule, len(a.config.HeaderRules))
	for i, input := range a.config.HeaderRules {
		entry := input
		entry.Headers = slices.Clone(input.Headers)
		entry.Has = slices.Clone(input.Has)
		entry.Missing = slices.Clone(input.Missing)
		owned[i] = entry
		matcher, err := compileRule(entry.Source, "/", entry.Has, entry.Missing)
		if err != nil {
			return fmt.Errorf("header rule %d: %w", i, err)
		}
		if len(entry.Headers) == 0 || len(entry.Headers) > 32 {
			return fmt.Errorf("header rule %d: require 1..32 header values", i)
		}
		names := map[string]bool{}
		for _, part := range segments(entry.Source) {
			if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
				names[strings.TrimSuffix(part[1:], "?")] = true
			}
		}
		for _, condition := range matcher.conditions {
			if !condition.missing && condition.expression != nil {
				for _, name := range condition.expression.SubexpNames() {
					if name != "" {
						names[name] = true
					}
				}
			}
		}
		rule := compiledHeaderRule{matchRule: matcher}
		for _, value := range entry.Headers {
			total += len(value.Name) + len(value.Value)
			if total > 128<<10 {
				return fmt.Errorf("header rule configuration exceeds 128 KiB")
			}
			if !allowedRuleResponseHeader(value.Name) {
				return fmt.Errorf("header rule %d: invalid or reserved response header", i)
			}
			parts, err := compileHeaderValue(value.Value, names)
			if err != nil {
				return fmt.Errorf("header rule %d: %w", i, err)
			}
			rule.values = append(rule.values, compiledHeaderValue{name: http.CanonicalHeaderKey(value.Name), append: value.Append, parts: parts})
		}
		a.headerRules = append(a.headerRules, rule)
	}
	a.config.HeaderRules = owned
	return nil
}
func canonicalHeaderRequest(r *http.Request) *http.Request {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	escaped := paths.Logical(u.EscapedPath())
	u.Path, _ = url.PathUnescape(escaped)
	u.RawPath = escaped
	return clone
}
func (a *App) headerRuleValues(r *http.Request) ([]HeaderValue, bool, error) {
	r = canonicalHeaderRequest(r)
	var output []HeaderValue
	total := 0
	sensitive := false
	for _, rule := range a.headerRules {
		if len(rule.matchRule.conditions) > 0 {
			if _, ok := match(rule.matchRule.source, r.URL.EscapedPath()); ok {
				sensitive = true
			}
		}
		params, ok := rule.matchRule.match(r)
		if !ok {
			continue
		}
		for _, value := range rule.values {
			var text strings.Builder
			for _, part := range value.parts {
				fragment := part.literal
				if part.name != "" {
					fragment = params[part.name]
				}
				if len(fragment) > 8192-text.Len() {
					return nil, sensitive, fmt.Errorf("resolved header value exceeds 8 KiB")
				}
				text.WriteString(fragment)
			}
			resolved := text.String()
			if !validRuleHeaderValue(resolved) {
				return nil, sensitive, fmt.Errorf("invalid resolved header value")
			}
			total += len(value.name) + len(resolved)
			if total > 32<<10 {
				return nil, sensitive, fmt.Errorf("resolved header rules exceed 32 KiB")
			}
			output = append(output, HeaderValue{Name: value.name, Value: resolved, Append: value.append})
		}
	}
	return output, sensitive, nil
}

// Bind before any route/handler work. Resolve the complete policy before
// applying it so an invalid capture cannot leave a partially applied policy.
func (a *App) bindHeaderRules(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, bool) {
	if len(a.headerRules) == 0 {
		return w, r, true
	}
	values, sensitive, err := a.headerRuleValues(r)
	if err != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
		http.Error(w, "Invalid response header rule", http.StatusBadRequest)
		return w, r, false
	}
	for _, value := range values {
		if value.Append {
			w.Header().Add(value.Name, value.Value)
		} else {
			w.Header().Set(value.Name, value.Value)
		}
	}
	if sensitive {
		r = r.WithContext(context.WithValue(r.Context(), sensitiveRuleKey{}, true))
		w = privateResponseWriter(w, false)
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
	}
	return w, r, true
}

// Locale negotiation/canonicalization owns its redirect before a child handler.
// Apply the selected scope's rules to the corresponding logical source path.
func (a *App) localeRedirectHeaders(w http.ResponseWriter, r *http.Request, logical string) (http.ResponseWriter, bool) {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	u.Path, _ = url.PathUnescape(logical)
	u.RawPath = logical
	out, _, ok := a.bindHeaderRules(w, clone)
	return out, ok
}
