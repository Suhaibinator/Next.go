package ui

import (
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestFramePolicyRejectsNonliteralOrigins(t *testing.T) {
	for _, origin := range []string{"*", "'self'", "//trusted.test", "https://*.trusted.test", "https://trusted.test/", "https://trusted.test/path", "https://trusted.test?", "https://trusted.test?x=1", "https://trusted.test#", "https://trusted.test#x", "https://user:secret@trusted.test", "javascript:alert(1)", "data:text/html,hello", " https://trusted.test", "https://trusted.test\n", "https://trusted.test\\@evil.test", "https://trusted.test:0", "https://trusted.test:65536", "https://trusted.test:0443", "https://trusted.test:", "https://[fe80::1%25en0]", "https://[example.com]", "https://[127.0.0.1]"} {
		if _, err := NewFramePolicy(FramePolicyOptions{Origins: []string{origin}}); err == nil {
			t.Errorf("accepted origin %q", origin)
		}
	}
	for _, origin := range []string{"https://trusted.test", "http://localhost:4100", "http://127.0.0.1:4100", "https://[::1]:443"} {
		if _, err := NewFramePolicy(FramePolicyOptions{Origins: []string{origin}}); err != nil {
			t.Errorf("rejected origin %q: %v", origin, err)
		}
	}
}

func TestFramePolicyURLAndCopiedSources(t *testing.T) {
	origins := []string{"https://trusted.test"}
	policy, err := NewFramePolicy(FramePolicyOptions{Origins: origins, AllowSelf: true})
	if err != nil {
		t.Fatal(err)
	}
	origins[0] = "https://evil.test"
	query := url.Values{"q": {`<img src=x onerror="bad">`}, "next": {"//evil.test"}}
	got, err := policy.URL("https://trusted.test", "/view?#literal", query)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil || u.Host != "trusted.test" || u.Path != "/view?#literal" || u.Query().Get("q") != query.Get("q") || u.Fragment != "" {
		t.Fatal(got, err)
	}
	local, err := policy.URL("", "/app/api/embed", nil)
	if err != nil || local != "/app/api/embed" {
		t.Fatal(local, err)
	}
	for _, test := range []struct{ origin, path string }{{"https://evil.test", "/view"}, {"//evil.test", "/view"}, {"https://trusted.test", "//evil.test"}, {"", "relative"}, {"", "/\\evil"}, {"", "/bad\npath"}} {
		if _, err := policy.URL(test.origin, test.path, nil); err == nil {
			t.Errorf("accepted URL construction %+v", test)
		}
	}
	sources := policy.CSPSources()
	if strings.Join(sources, " ") != "'self' https://trusted.test" {
		t.Fatal(sources)
	}
	sources[1] = "https://evil.test"
	if strings.Join(policy.CSPSources(), " ") != "'self' https://trusted.test" {
		t.Fatal("caller mutated policy")
	}
}

func TestFrameDefaultsEscapeAndRemainSandboxed(t *testing.T) {
	policy, _ := NewFramePolicy(FramePolicyOptions{Origins: []string{"https://trusted.test"}, AllowSelf: true})
	got, err := HTML(policy.Frame(FrameOptions{Src: "https://trusted.test/player?x=1&y=2", Title: `A "quoted" <frame>`, ID: `"><img src=x>`, Width: 640, Height: 360}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`sandbox=""`, `loading="lazy"`, `referrerpolicy="no-referrer"`, `width="640"`, `height="360"`, `title="A &#34;quoted&#34; &lt;frame&gt;"`, `src="https://trusted.test/player?x=1&amp;y=2"`, "camera &#39;none&#39;", "fullscreen &#39;none&#39;", `Open A &#34;quoted&#34; &lt;frame&gt;`, `data-nextgo-reload="true"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "<img") || strings.Contains(got, "<script") || strings.Contains(got, "srcdoc") {
		t.Fatal(got)
	}
	got, err = HTML(policy.Frame(FrameOptions{Src: "/api/embed", Title: "Local", Eager: true, Sandbox: FrameSandbox{Scripts: true, Forms: true}, Permissions: FramePermissions{Fullscreen: true}, ReferrerPolicy: "same-origin", Fallback: Text("Visible fallback")}))
	if err != nil || !strings.Contains(got, `sandbox="allow-scripts allow-forms"`) || !strings.Contains(got, "fullscreen &#39;src&#39;") || !strings.HasSuffix(got, "></iframe>Visible fallback") {
		t.Fatal(got, err)
	}
	if _, err := HTML(El("iframe", Attrs{"src": "/api/embed"})); err == nil {
		t.Fatal("general El iframe protection was weakened")
	}
}

func TestFrameRejectsPolicyBypassesBeforeOutput(t *testing.T) {
	policy, _ := NewFramePolicy(FramePolicyOptions{Origins: []string{"https://trusted.test"}})
	for _, src := range []string{"/api/embed", "//trusted.test/frame", "https://evil.test/frame", "https://trusted.test.evil.test/frame", "https://trusted.test:443/frame", "http://trusted.test/frame", "https://trusted.test@evil.test/frame", "https://user@trusted.test/frame", "https://trusted.test\\@evil.test/frame", "javascript:alert(1)", "java\nscript:alert(1)", "data:text/html,hello", "blob:https://trusted.test/id", "about:blank", "relative", "/\\evil.test/frame"} {
		got, err := HTML(policy.Frame(FrameOptions{Src: src, Title: "Test"}))
		if err == nil || got != "" {
			t.Errorf("accepted %q or partially rendered: %q %v", src, got, err)
		}
	}
	for _, options := range []FrameOptions{{Src: "https://trusted.test", Title: ""}, {Src: "https://trusted.test", Title: "Test", Sandbox: FrameSandbox{Scripts: true, SameOrigin: true}}, {Src: "https://trusted.test", Title: "Test", Width: -1}, {Src: "https://trusted.test", Title: "Test", ReferrerPolicy: "unsafe-url"}} {
		if got, err := HTML(policy.Frame(options)); err == nil || got != "" {
			t.Fatal(got, err)
		}
	}
	var nilPolicy *FramePolicy
	if got, err := HTML(nilPolicy.Frame(FrameOptions{Src: "/frame", Title: "Test"})); err == nil || got != "" {
		t.Fatal(got, err)
	}
	closed, _ := NewFramePolicy(FramePolicyOptions{})
	if _, err := closed.URL("", "/frame", nil); err == nil {
		t.Fatal("closed policy built local URL")
	}
}

func TestFrameConcurrentImmutableRender(t *testing.T) {
	policy, _ := NewFramePolicy(FramePolicyOptions{AllowSelf: true})
	node := policy.Frame(FrameOptions{Src: "/frame", Title: "Test"})
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				if err := node.Render(io.Discard); err != nil {
					t.Error(err)
				}
				_ = policy.CSPSources()
			}
		}()
	}
	workers.Wait()
}

func FuzzFrameTitleEscaping(f *testing.F) {
	f.Add(`"><script>alert(1)</script>`)
	f.Add("Ordinary player")
	policy, _ := NewFramePolicy(FramePolicyOptions{AllowSelf: true})
	f.Fuzz(func(t *testing.T, title string) {
		got, err := HTML(policy.Frame(FrameOptions{Src: "/frame", Title: title, Fallback: Text("Fallback")}))
		if err != nil {
			return
		}
		if strings.Count(got, "<") != 2 || strings.Count(got, ">") != 2 {
			t.Fatal(got)
		}
	})
}
