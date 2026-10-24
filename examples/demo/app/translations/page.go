//go:build !js

package translations

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

type messages struct{ title, body, next, second string }

var dictionary = map[string]messages{
	"en": {"Hello", "This content was selected by a Go server loader using the request locale.", "Next step", "Second step"},
	"fr": {"Bonjour", "Ce contenu a été choisi par un chargeur Go selon la langue de la requête.", "Étape suivante", "Deuxième étape"},
	"ar": {"مرحبا", "اختار خادم Go هذا المحتوى حسب لغة الطلب.", "الخطوة التالية", "الخطوة الثانية"},
}

func Page(c *nextgo.Context) (nextgo.Page, error) {
	code := nextgo.CurrentLocale(c)
	if code == "" {
		code = "en"
	}
	text := dictionary[code]
	body := text.body
	if c.Query.Get("view") == "second" {
		body = text.second
	}
	links := []ui.Node{}
	alternates := map[string]string{}
	for _, language := range nextgo.AvailableLocales(c) {
		href, err := nextgo.LocaleURL(c, language, "/translations")
		if err != nil {
			return nextgo.Page{}, err
		}
		links = append(links, ui.El("a", ui.Attrs{"href": href, "lang": language, "data-language": language}, ui.Text(language)))
		alternates[language] = href
	}
	canonical := ""
	if nextgo.CurrentLocale(c) != "" {
		canonical = nextgo.MustLocaleURL(c, code, "/translations")
	}
	return nextgo.Page{Metadata: seo.Metadata{Title: text.title, Alternates: alternates, Canonical: canonical}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", ui.Attrs{"data-translated-heading": ""}, ui.Text(text.title)),
			ui.El("p", ui.Attrs{"data-translated-body": ""}, ui.Text(body)), ui.El("nav", ui.Attrs{"aria-label": "Languages"}, links...),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/translations?view=second"), "data-locale-next": ""}, ui.Text(text.next))),
		counter.View(counter.Props{Label: "Go counter", Initial: 0}),
	)}, nil
}
