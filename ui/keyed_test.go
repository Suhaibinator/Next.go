package ui

import (
	"strings"
	"testing"
)

func TestKeyedUsesNaturalElementAndEscapesWithoutMutatingAttributes(t *testing.T) {
	attrs := Attrs{"class": "row"}
	html, err := HTML(Keyed("li", `a"<b`, attrs, Text("<label>")))
	if err != nil || html != `<li class="row" data-nextgo-key="a&#34;&lt;b">&lt;label&gt;</li>` || len(attrs) != 1 {
		t.Fatal(html, err, attrs)
	}
	for _, node := range []Node{Keyed("li", "", nil), Keyed("li", "x", Attrs{"DATA-NEXTGO-KEY": "y"})} {
		if _, err := HTML(node); err == nil {
			t.Fatal("invalid keyed markup accepted")
		}
	}
	if _, err := HTML(Keyed("script", "x", nil, Text("bad"))); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
}
