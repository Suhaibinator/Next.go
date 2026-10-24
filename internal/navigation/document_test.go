package navigation

import (
	"strings"
	"testing"
)

func TestDocumentLanguageDirectionAndBounds(t *testing.T) {
	for _, language := range []string{"en", "fr-CA", "zh-Hant", "sr-Latn-RS", "ar", "x-demo", "i-klingon"} {
		if err := (Document{Language: language, Direction: "ltr", Viewport: DefaultViewport}).Validate(); err != nil {
			t.Fatal(language, err)
		}
	}
	for _, language := range []string{"", "e", "en_US", "en--US", "en-abcdefghi", "en-<script>", "en-été", strings.Repeat("en-", 86) + "en"} {
		if ValidLanguage(language) {
			t.Fatal("invalid language accepted", language)
		}
	}
	for _, document := range []Document{{Language: "en", Direction: "unexpected", Viewport: DefaultViewport}, {Language: "en", Direction: "ltr"}, {Language: "en", Direction: "ltr", Viewport: strings.Repeat("a", 2049)}, {Language: "en", Direction: "ltr", Viewport: "width=1\nscript"}} {
		if document.Validate() == nil {
			t.Fatal("invalid document accepted", document)
		}
	}
}
