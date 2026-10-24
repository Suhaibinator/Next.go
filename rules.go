package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/routing"
)

// ConditionType selects an actual request input. Host uses Request.Host without
// its port; forwarded headers never override it.
type ConditionType string

const (
	HeaderCondition ConditionType = "header"
	CookieCondition ConditionType = "cookie"
	QueryCondition  ConditionType = "query"
	HostCondition   ConditionType = "host"
)

// RuleCondition matches presence when Value is empty, otherwise a whole-value
// Go/RE2 regular expression. Named groups in Has can fill destination parameters.
// Missing negates the match and cannot supply captured values. Use ^$ to match
// an explicitly empty value rather than mere presence.
type RuleCondition struct {
	Type       ConditionType
	Key, Value string
}

// RedirectRule uses app-relative source patterns. Destination is an app-relative
// path or a literal HTTP(S) origin plus path. 307/308 preserve request methods.
type RedirectRule struct {
	Source, Destination string
	Permanent           bool
	Has, Missing        []RuleCondition
}

// RewriteRule changes internal routing without changing the public URL. HTTP(S)
// destinations use Config.Proxy budgets and cannot interpolate their authority.
type RewriteRule struct {
	Source, Destination string
	Has, Missing        []RuleCondition
}

type ruleCondition struct {
	kind       ConditionType
	key        string
	expression *regexp.Regexp
	missing    bool
}
type compiledRule struct {
	source     string
	target     url.URL
	hasQuery   bool
	conditions []ruleCondition
	status     int
}

func (rule compiledRule) external() bool { return rule.target.Scheme != "" }

type sensitiveRuleKey struct{}

func ruleSensitive(ctx context.Context) bool {
	value, _ := ctx.Value(sensitiveRuleKey{}).(bool)
	return value
}
func (a *App) ruleSensitive(r *http.Request) bool {
	if len(a.headerRules) > 0 {
		source := canonicalHeaderRequest(r)
		for _, rule := range a.headerRules {
			if len(rule.matchRule.conditions) > 0 {
				if _, ok := match(rule.matchRule.source, source.URL.EscapedPath()); ok {
					return true
				}
			}
		}
	}
	for _, rules := range [][]compiledRule{a.redirects, a.rewrites} {
		for _, rule := range rules {
			if len(rule.conditions) > 0 {
				if _, ok := routing.Match(rule.source, r.URL.EscapedPath()); ok {
					return true
				}
			}
		}
	}
	return false
}

var ruleIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func compileRule(source, destination string, has, missing []RuleCondition) (compiledRule, error) {
	rule := compiledRule{source: strings.Clone(source)}
	if len(source) > 4096 || len(destination) > 8192 || len(has)+len(missing) > 16 {
		return rule, fmt.Errorf("rule exceeds source/destination/condition bounds")
	}
	if _, err := routing.Validate(source); err != nil {
		return rule, fmt.Errorf("invalid source pattern")
	}
	names := map[string]bool{}
	for _, segment := range strings.Split(source, "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			names[strings.TrimSuffix(segment[1:], "?")] = true
		}
	}
	for phase, conditions := range [][]RuleCondition{has, missing} {
		for _, condition := range conditions {
			if len(condition.Key) > 256 || len(condition.Value) > 2048 {
				return rule, fmt.Errorf("condition key or expression exceeds bounds")
			}
			c := ruleCondition{kind: condition.Type, key: strings.Clone(condition.Key), missing: phase == 1}
			switch c.kind {
			case HeaderCondition:
				if !validRuleHeader(c.key) {
					return rule, fmt.Errorf("invalid condition header")
				}
				c.key = http.CanonicalHeaderKey(c.key)
			case CookieCondition:
				if err := (&http.Cookie{Name: c.key, Value: "x"}).Valid(); err != nil {
					return rule, fmt.Errorf("invalid condition cookie")
				}
			case QueryCondition:
				if c.key == "" || strings.ContainsAny(c.key, "\r\n\x00") || !utf8.ValidString(c.key) {
					return rule, fmt.Errorf("invalid condition query key")
				}
			case HostCondition:
				if c.key != "" {
					return rule, fmt.Errorf("host condition must omit Key")
				}
			default:
				return rule, fmt.Errorf("unknown condition type")
			}
			if condition.Value != "" {
				expression, err := regexp.Compile("^(?:" + condition.Value + ")$")
				if err != nil {
					return rule, fmt.Errorf("invalid Go regular expression")
				}
				c.expression = expression
				for _, name := range expression.SubexpNames() {
					if name != "" && !c.missing {
						if !ruleIdentifier.MatchString(name) || names[name] {
							return rule, fmt.Errorf("invalid or colliding named capture")
						}
						names[name] = true
					}
				}
			}
			rule.conditions = append(rule.conditions, c)
		}
	}
	target, err := url.Parse(destination)
	if err != nil || target.User != nil || target.Opaque != "" || strings.ContainsAny(destination, "\\\r\n\x00") {
		return rule, fmt.Errorf("invalid destination URL")
	}
	if target.Scheme != "" || target.Host != "" {
		target.Scheme = strings.ToLower(target.Scheme)
		if target.Scheme != "http" && target.Scheme != "https" {
			return rule, fmt.Errorf("destination requires HTTP(S)")
		}
		authority, err := paths.CanonicalHost(target.Host)
		if err != nil {
			return rule, fmt.Errorf("destination authority must be literal HTTP(S) host")
		}
		target.Host = authority
		if target.Path == "" {
			target.Path = "/"
		}
	} else if !strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "//") {
		return rule, fmt.Errorf("destination must be app-absolute or HTTP(S)")
	}
	if _, err := (paths.Config{}).URL(target.EscapedPath()); err != nil {
		return rule, fmt.Errorf("invalid destination path")
	}
	for _, part := range strings.Split(target.EscapedPath(), "/") {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
			if !ruleIdentifier.MatchString(part[1:]) || !names[part[1:]] {
				return rule, fmt.Errorf("unknown destination path parameter")
			}
		}
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return rule, fmt.Errorf("invalid destination query")
	}
	for _, values := range query {
		for _, value := range values {
			if strings.HasPrefix(value, ":") && ruleIdentifier.MatchString(value[1:]) && !names[value[1:]] {
				return rule, fmt.Errorf("unknown destination query parameter")
			}
		}
	}
	if strings.HasPrefix(target.Fragment, ":") && ruleIdentifier.MatchString(target.Fragment[1:]) && !names[target.Fragment[1:]] {
		return rule, fmt.Errorf("unknown destination fragment parameter")
	}
	rule.target = *target
	rule.hasQuery = target.RawQuery != "" || target.ForceQuery
	return rule, nil
}
func validRuleHeader(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
func (condition ruleCondition) values(r *http.Request) ([]string, bool) {
	switch condition.kind {
	case HeaderCondition:
		return r.Header.Values(condition.key), true
	case CookieCondition:
		values := []string{}
		for _, cookie := range r.Cookies() {
			if cookie.Name == condition.key {
				values = append(values, cookie.Value)
			}
		}
		return values, true
	case QueryCondition:
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return nil, false
		}
		return values[condition.key], true
	case HostCondition:
		authority, err := paths.CanonicalHost(r.Host)
		if err != nil {
			return nil, false
		}
		return []string{(&url.URL{Host: authority}).Hostname()}, true
	}
	return nil, false
}
func (rule compiledRule) match(r *http.Request) (map[string]string, bool) {
	params, ok := routing.Match(rule.source, r.URL.EscapedPath())
	if !ok {
		return nil, false
	}
	for _, condition := range rule.conditions {
		found := false
		values, valid := condition.values(r)
		if !valid {
			return nil, false
		}
		for _, value := range values {
			if len(value) > 8192 && condition.expression != nil {
				return nil, false
			}
			if condition.expression == nil {
				found = true
				break
			}
			groups := condition.expression.FindStringSubmatch(value)
			if groups == nil {
				continue
			}
			found = true
			if !condition.missing {
				for i, name := range condition.expression.SubexpNames() {
					if name != "" {
						params[name] = groups[i]
					}
				}
			}
			break
		}
		if found == condition.missing {
			return nil, false
		}
	}
	return params, true
}
func (rule compiledRule) destination(params map[string]string, incoming *url.URL) (*url.URL, error) {
	target := rule.target
	parts := strings.Split(target.EscapedPath(), "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = url.PathEscape(params[part[1:]])
		}
		if strings.HasPrefix(part, "*") {
			values := strings.Split(params[part[1:]], "/")
			for j, value := range values {
				values[j] = url.PathEscape(value)
			}
			parts[i] = strings.Join(values, "/")
			if parts[i] == "" && i == len(parts)-1 {
				parts = parts[:i]
			}
		}
	}
	escaped := strings.Join(parts, "/")
	if escaped == "" {
		escaped = "/"
	}
	if _, err := (paths.Config{}).URL(escaped); err != nil {
		return nil, fmt.Errorf("invalid resolved rule path")
	}
	target.Path, _ = url.PathUnescape(escaped)
	target.RawPath = escaped
	query, _ := url.ParseQuery(target.RawQuery)
	changed := false
	for key, values := range query {
		for i, value := range values {
			if strings.HasPrefix(value, ":") && ruleIdentifier.MatchString(value[1:]) {
				query[key][i] = params[value[1:]]
				changed = true
			}
		}
	}
	if changed {
		target.RawQuery = query.Encode()
	}
	if !rule.hasQuery {
		target.RawQuery, target.ForceQuery = incoming.RawQuery, incoming.ForceQuery
	}
	if strings.HasPrefix(target.Fragment, ":") && ruleIdentifier.MatchString(target.Fragment[1:]) {
		target.Fragment = params[target.Fragment[1:]]
		target.RawFragment = ""
	}
	if len(target.String()) > 16384 {
		return nil, fmt.Errorf("resolved rule URL exceeds bounds")
	}
	return &target, nil
}
func (a *App) initRules() error {
	if len(a.config.Redirects)+len(a.config.Rewrites)+len(a.config.HeaderRules) > 256 {
		return fmt.Errorf("nextgo: at most 256 request rules")
	}
	if err := a.initHeaderRules(); err != nil {
		return err
	}
	for i, entry := range a.config.Redirects {
		rule, err := compileRule(entry.Source, entry.Destination, entry.Has, entry.Missing)
		if err != nil {
			return fmt.Errorf("redirect rule %d: %w", i, err)
		}
		rule.status = 307
		if entry.Permanent {
			rule.status = 308
		}
		a.redirects = append(a.redirects, rule)
	}
	if err := validateRedirectCycles(a.redirects); err != nil {
		return err
	}
	external := false
	for i, entry := range a.config.Rewrites {
		rule, err := compileRule(entry.Source, entry.Destination, entry.Has, entry.Missing)
		if err != nil {
			return fmt.Errorf("rewrite rule %d: %w", i, err)
		}
		if rule.target.Fragment != "" {
			return fmt.Errorf("rewrite rule %d: fragments have no upstream request meaning", i)
		}
		external = external || rule.external()
		a.rewrites = append(a.rewrites, rule)
	}
	if external {
		var err error
		a.proxy, err = newRewriteProxy(a.config.Proxy)
		if err != nil {
			return err
		}
	}
	return nil
}
func (a *App) rulesIdentity() string {
	if len(a.redirects)+len(a.rewrites)+len(a.headerRules) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(struct {
		Redirects []RedirectRule
		Rewrites  []RewriteRule
		Proxy     ProxyOptions
		Headers   []HeaderRule
	}{a.config.Redirects, a.config.Rewrites, a.config.Proxy, a.config.HeaderRules})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Exact unconditional local redirects can be checked without request inputs.
// Parameterized/conditional/external cycles still require deployment tests.
func validateRedirectCycles(rules []compiledRule) error {
	edges := map[string]string{}
	for _, rule := range rules {
		if !rule.external() && len(rule.conditions) == 0 {
			if paths.Logical(rule.target.Path) == rule.source {
				return fmt.Errorf("nextgo: unconditional redirect targets its own source")
			}
			if !strings.ContainsAny(rule.source+rule.target.Path, ":*") {
				if _, exists := edges[rule.source]; !exists {
					edges[rule.source] = paths.Logical(rule.target.Path)
				}
			}
		}
	}
	for source := range edges {
		seen := map[string]bool{}
		for current := source; edges[current] != ""; current = edges[current] {
			if seen[current] {
				return fmt.Errorf("nextgo: unconditional local redirect cycle")
			}
			seen[current] = true
		}
	}
	return nil
}
