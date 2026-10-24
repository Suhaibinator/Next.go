package nextgo

import (
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestViewTransitionPolicyIsOptInAndVersionsDocuments(t *testing.T) {
	plain := testApp(t, Config{BuildID: "same-build", Routes: []Route{{Pattern: "/", Page: static("HTML before WASM")}}})
	animated := testApp(t, Config{BuildID: "same-build", ViewTransitions: true, Routes: []Route{{Pattern: "/", Page: static("HTML before WASM")}}})
	if plain.configurationID == animated.configurationID {
		t.Fatal("transition policy was omitted from configuration identity")
	}
	for _, app := range []*App{plain, animated} {
		response := request(app, "/", false)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "HTML before WASM") {
			t.Fatal(response.Code, response.Body.String())
		}
		enabled := strings.Contains(response.Body.String(), `data-nextgo-transitions="true"`)
		if enabled != app.config.ViewTransitions {
			t.Fatal("wrong document transition policy")
		}
	}
	stream := testApp(t, Config{BuildID: "same-build", ViewTransitions: true, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Text("Streamed HTML")}, nil
	}}}})
	response := request(stream, "/", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `data-nextgo-transitions="true"`) || !strings.Contains(response.Body.String(), "Streamed HTML") {
		t.Fatal("streamed document lost its transition policy", response.Code, response.Body.String())
	}
}
