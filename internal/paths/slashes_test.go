package paths

import "testing"

func TestSlashURLPolicyAndExceptions(t *testing.T) {
	for _, base := range []string{"", "/docs"} {
		for _, test := range []struct{ input, off, on string }{
			{"/", "/", "/"}, {"/about", "/about", "/about/"}, {"/about/", "/about", "/about/"},
			{"/posts/a%3Fb?q=x%26y#detail", "/posts/a%3Fb?q=x%26y#detail", "/posts/a%3Fb/?q=x%26y#detail"},
			{"/api/health", "/api/health", "/api/health/"}, {"/file.txt/", "/file.txt", "/file.txt"},
			{"/files/a%2Eb/", "/files/a%2Eb", "/files/a%2Eb"}, {"/.well-known/token", "/.well-known/token", "/.well-known/token"},
			{"/.well-known/folder/", "/.well-known/folder", "/.well-known/folder"},
			{"/_nextgo/loading", "/_nextgo/loading", "/_nextgo/loading"}, {"/_nextgo/actions/save/", "/_nextgo/actions/save", "/_nextgo/actions/save"},
			{"/assets/picture", "/assets/picture", "/assets/picture"}, {"/about?", "/about?", "/about/?"},
		} {
			for _, on := range []bool{false, true} {
				c := Config{BasePath: base, TrailingSlash: on}
				want := test.off
				if on {
					want = test.on
				}
				if base != "" && want == "/" && !on {
					want = ""
				}
				want = base + want
				actual, err := c.URL(test.input)
				if err != nil || actual != want {
					t.Errorf("base=%q slash=%v %q: %q %v; want %q", base, on, test.input, actual, err, want)
				}
			}
		}
	}
	c := Config{BasePath: "/docs", AssetPrefix: "https://cdn.example.com", TrailingSlash: true}
	if got, err := c.AssetURL("/logo.svg"); err != nil || got != "https://cdn.example.com/assets/logo.svg" {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"/about//", "//foreign/", "/a%2Fb/", "/a/../b/"} {
		if _, err := c.URL(bad); err == nil {
			t.Error("accepted malformed URL", bad)
		}
	}
}
