//go:build !js

package locale

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

func Metadata(c *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	current := selected(c)
	return seo.Metadata{Title: current.title, Language: current.language, Direction: current.direction, Canonical: nextgo.MustURL(c, "/locale?lang="+current.language), Alternates: map[string]string{"en": nextgo.MustURL(c, "/locale?lang=en"), "fr": nextgo.MustURL(c, "/locale?lang=fr"), "ar": nextgo.MustURL(c, "/locale?lang=ar")}, Viewport: &seo.Viewport{Fit: "cover", InteractiveWidget: "resizes-content", ColorScheme: "light dark", ThemeColors: []seo.ThemeColor{{Color: "#126d64"}, {Color: "#071b18", Media: "(prefers-color-scheme: dark)"}}}}, nil
}
