//go:build !js

package locale

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

type translation struct{ language, direction, title, body string }

func selected(c *nextgo.Context) translation {
	switch c.Query.Get("lang") {
	case "fr":
		return translation{"fr", "ltr", "Bonjour", "Le serveur définit la langue, le sens du texte et les métadonnées avant WebAssembly."}
	case "ar":
		return translation{"ar", "rtl", "مرحبا", "يحدد الخادم لغة المستند واتجاهه وبياناته الوصفية قبل تحميل WebAssembly."}
	default:
		return translation{"en", "ltr", "Hello", "The server sets document language, direction and metadata before WebAssembly loads."}
	}
}

func Page(c *nextgo.Context) (nextgo.Page, error) {
	current := selected(c)
	links := []ui.Node{}
	for _, language := range []struct{ code, label string }{{"en", "English"}, {"fr", "Français"}, {"ar", "العربية"}} {
		attrs := ui.Attrs{"href": nextgo.MustURL(c, "/locale?lang="+language.code), "lang": language.code}
		if current.language == language.code {
			attrs["aria-current"] = "page"
		}
		links = append(links, ui.El("a", attrs, ui.Text(language.label)), ui.Text(" · "))
	}
	return nextgo.Page{Body: ui.El("section", ui.Attrs{"class": "prose", "data-document-example": ""}, ui.El("h1", nil, ui.Text(current.title)), ui.El("p", nil, ui.Text(current.body)), ui.El("nav", ui.Attrs{"aria-label": "Document language"}, links...), ui.El("p", ui.Attrs{"lang": "en", "dir": "ltr"}, ui.Text("This explicit query-language example does not provide locale negotiation, translated route discovery or domain routing.")))}, nil
}
