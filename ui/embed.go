package ui

import (
	"fmt"
	"html"
	"io"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// FramePolicyOptions grants initial iframe destinations. Origins must be
// literal HTTP(S) origins, with no wildcard, credentials, path, query or fragment.
// AllowSelf separately grants root-relative URLs; it never trusts a request Host.
type FramePolicyOptions struct {
	Origins   []string
	AllowSelf bool
}

// FramePolicy is immutable after construction and safe for concurrent renders.
// Browser CSP must also constrain frame-src: redirects are not checked by Go.
type FramePolicy struct {
	origins map[string]bool
	self    bool
}

func NewFramePolicy(options FramePolicyOptions) (*FramePolicy, error) {
	p := &FramePolicy{origins: make(map[string]bool), self: options.AllowSelf}
	for _, origin := range options.Origins {
		u, err := resourceURL(origin)
		if err != nil || u.Scheme == "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") {
			return nil, fmt.Errorf("ui: frame policy requires literal HTTP(S) origins")
		}
		p.origins[u.Scheme+"://"+u.Host] = true
	}
	return p, nil
}

// CSPSources returns a sorted owned copy suitable for an application frame-src
// directive. An empty result should be represented as 'none' in that directive.
func (p *FramePolicy) CSPSources() []string {
	var sources []string
	if p == nil {
		return sources
	}
	for origin := range p.origins {
		sources = append(sources, origin)
	}
	if p.self {
		sources = append(sources, "'self'")
	}
	sort.Strings(sources)
	return sources
}

// URL constructs an allowed URL without interpreting path as an authority or
// query. Use an empty origin only with AllowSelf. Path is a decoded absolute
// path, so a literal '?' or '#' is escaped rather than becoming URL syntax.
func (p *FramePolicy) URL(origin, path string, query url.Values) (string, error) {
	if p == nil || (origin == "" && !p.self) || (origin != "" && !p.origins[origin]) {
		return "", fmt.Errorf("ui: frame origin is not allowed")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") || hasResourceWhitespace(path) {
		return "", fmt.Errorf("ui: frame path must be root-relative")
	}
	u := &url.URL{Path: path, RawQuery: query.Encode()}
	return origin + u.String(), nil
}

// FrameSandbox lists the only sandbox grants supported here. The empty value
// retains all sandbox restrictions. Scripts with SameOrigin are always denied;
// popups escaping the sandbox and top navigation are deliberately unavailable.
type FrameSandbox struct {
	Scripts, SameOrigin, Forms, Popups, Downloads, Presentation, PointerLock bool
}

// FramePermissions grants selected capabilities only to the frame's src origin.
// Camera, microphone, location, payment, USB and clipboard remain denied.
type FramePermissions struct {
	Fullscreen, Autoplay, PictureInPicture bool
}

type FrameOptions struct {
	Src, Title, ID, Class string
	Width, Height         int
	Eager                 bool
	Sandbox               FrameSandbox
	Permissions           FramePermissions
	// ReferrerPolicy defaults to no-referrer. Only no-referrer, same-origin and
	// strict-origin are supported, avoiding full cross-origin URL disclosure.
	ReferrerPolicy string
	// Fallback follows the iframe and stays visible even when framing is blocked.
	// A nil fallback supplies a link using Title and the validated Src.
	Fallback Node
}

// Frame is the sanctioned iframe renderer. El("iframe", ...) remains forbidden.
// Invalid options fail before any HTML is written. There is no srcdoc or raw
// attribute escape hatch. This renders native browser HTML and adds no app JS.
func (p *FramePolicy) Frame(options FrameOptions) Node {
	u, err := resourceURL(options.Src)
	if err == nil && (p == nil || (u.Scheme == "" && !p.self) || (u.Scheme != "" && !p.origins[u.Scheme+"://"+u.Host])) {
		err = fmt.Errorf("ui: frame source is not allowed")
	}
	if err != nil {
		return resourceError(err)
	}
	if strings.TrimSpace(options.Title) == "" || len(options.Title) > 1024 || options.Width < 0 || options.Height < 0 {
		return resourceError(fmt.Errorf("ui: frame requires a title and nonnegative dimensions"))
	}
	if options.Sandbox.Scripts && options.Sandbox.SameOrigin {
		return resourceError(fmt.Errorf("ui: frame scripts and same-origin cannot be combined"))
	}
	referrer := options.ReferrerPolicy
	if referrer == "" {
		referrer = "no-referrer"
	}
	if referrer != "no-referrer" && referrer != "same-origin" && referrer != "strict-origin" {
		return resourceError(fmt.Errorf("ui: unsupported frame referrer policy"))
	}
	var sandbox []string
	for _, grant := range []struct {
		allowed bool
		name    string
	}{{options.Sandbox.Scripts, "allow-scripts"}, {options.Sandbox.SameOrigin, "allow-same-origin"}, {options.Sandbox.Forms, "allow-forms"}, {options.Sandbox.Popups, "allow-popups"}, {options.Sandbox.Downloads, "allow-downloads"}, {options.Sandbox.Presentation, "allow-presentation"}, {options.Sandbox.PointerLock, "allow-pointer-lock"}} {
		if grant.allowed {
			sandbox = append(sandbox, grant.name)
		}
	}
	allow := "camera 'none'; microphone 'none'; geolocation 'none'; payment 'none'; usb 'none'; clipboard-read 'none'; clipboard-write 'none'"
	for _, permission := range []struct {
		allowed bool
		name    string
	}{{options.Permissions.Fullscreen, "fullscreen"}, {options.Permissions.Autoplay, "autoplay"}, {options.Permissions.PictureInPicture, "picture-in-picture"}} {
		grant := "'none'"
		if permission.allowed {
			grant = "'src'"
		}
		allow += "; " + permission.name + " " + grant
	}
	attrs := Attrs{"src": options.Src, "title": options.Title, "sandbox": strings.Join(sandbox, " "), "allow": allow, "referrerpolicy": referrer, "loading": "lazy"}
	if options.Eager {
		attrs["loading"] = "eager"
	}
	resourceIdentity(attrs, options.ID, options.Class)
	resourceDimensions(attrs, options.Width, options.Height)
	// Only these framework-owned keys reach this serializer. Values are escaped
	// exactly as in El; general callers cannot use it to render forbidden tags.
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var markup strings.Builder
	markup.WriteString("<iframe")
	for _, key := range keys {
		markup.WriteString(" " + key + `="` + html.EscapeString(attrs[key]) + `"`)
	}
	markup.WriteString("></iframe>")
	frame := nodeFunc(func(w io.Writer) error { _, err := io.WriteString(w, markup.String()); return err })
	fallback := options.Fallback
	if fallback == nil {
		fallback = El("a", Attrs{"href": options.Src, "rel": "noreferrer noopener", "data-nextgo-reload": "true"}, Text("Open "+options.Title))
	}
	return Fragment(frame, fallback)
}

func resourceError(err error) Node { return nodeFunc(func(io.Writer) error { return err }) }

func hasResourceWhitespace(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// resourceURL excludes URL forms whose browser authority interpretation differs
// from ordinary net/url parsing. It accepts root-relative or literal HTTP(S).
func resourceURL(value string) (*url.URL, error) {
	if value == "" || len(value) > 8192 || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") || hasResourceWhitespace(value) {
		return nil, fmt.Errorf("ui: require an HTTP(S) or root-relative resource URL")
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("ui: invalid resource URL")
	}
	if u.Scheme == "" {
		if u.Host != "" || !strings.HasPrefix(value, "/") {
			return nil, fmt.Errorf("ui: require a root-relative resource URL")
		}
		return u, nil
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("ui: resource scheme must be HTTP(S)")
	}
	host := u.Hostname()
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("ui: resource hosts cannot contain zone identifiers")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Is6() != strings.HasPrefix(u.Host, "[") {
			return nil, fmt.Errorf("ui: invalid resource IP brackets")
		}
	} else {
		if strings.ContainsAny(u.Host, "[]") {
			return nil, fmt.Errorf("ui: invalid resource host brackets")
		}
		if host == "" || strings.ContainsAny(host, ":%") {
			return nil, fmt.Errorf("ui: invalid resource host")
		}
		for _, r := range host {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
				return nil, fmt.Errorf("ui: invalid resource host")
			}
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return nil, fmt.Errorf("ui: invalid resource port")
		}
	}
	return u, nil
}

func resourceIdentity(attrs Attrs, id, class string) {
	if id != "" {
		attrs["id"] = id
	}
	if class != "" {
		attrs["class"] = class
	}
}

func resourceDimensions(attrs Attrs, width, height int) {
	if width > 0 {
		attrs["width"] = strconv.Itoa(width)
	}
	if height > 0 {
		attrs["height"] = strconv.Itoa(height)
	}
}
