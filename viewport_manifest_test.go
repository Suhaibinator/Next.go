package nextgo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestViewportDevelopmentPolicyBindsDocumentConfiguration(t *testing.T) {
	var identities []string
	for _, development := range []bool{false, true} {
		app := testApp(t, Config{BuildID: "same-build", Dev: development, Routes: []Route{
			{Pattern: "/", Page: func(*Context) (Page, error) {
				return Page{Body: ui.Link("/about", ui.LinkOptions{}, ui.Text("About"))}, nil
			}},
			{Pattern: "/about", Page: static("About")},
		}})
		var manifest navigation.Manifest
		if err := json.Unmarshal([]byte(app.navigationJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Development != development {
			t.Fatalf("development=%v: browser policy=%v", development, manifest.Development)
		}
		response := request(app, "/", false)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `data-nextgo-prefetch="viewport"`) || !strings.Contains(response.Body.String(), ">About</a>") {
			t.Fatalf("development=%v: meaningful native link missing: %d", development, response.Code)
		}
		if !strings.Contains(response.Body.String(), app.configurationID) {
			t.Fatal("configuration identity missing from document")
		}
		identities = append(identities, app.configurationID)
	}
	if identities[0] == identities[1] {
		t.Fatal("development automatic-prefetch policy reused production ownership identity")
	}
}
