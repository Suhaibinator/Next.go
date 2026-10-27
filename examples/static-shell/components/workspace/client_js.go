//go:build js && wasm

package workspace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/ui"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	return MountView(scope, raw, "workspace")
}

// MountView is shared by eager/core and lazy/module entrypoints. It imports no
// native page or startup settings package; both targets read committed URLs.
func MountView(scope *client.Scope, raw json.RawMessage, component string) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	location, ready := client.CurrentLocation()
	params, matched := client.CurrentRouteParams("/workspace/*rest?")
	if !ready || !matched {
		return fmt.Errorf("workspace mount requires a committed workspace URL")
	}
	view, err := scope.ClientView(ui.El("main", ui.Attrs{"data-shell-view": "", "data-shell-component": component},
		ui.El("h1", nil, ui.Text(p.Label)),
		ui.El("p", nil, ui.Text("Created in Go after the actual target URL committed. Cross-page and query navigation remount this view and reset its local counter.")),
		ui.El("dl", nil,
			field("Pathname", "pathname", location.Pathname),
			field("Decoded rest", "rest", params["rest"]),
			field("Decoded query values", "query", queryText(location)),
			field("Decoded fragment", "hash", fragmentText(location.Hash)),
			field("Rest observed during Mount", "mount-rest", params["rest"])),
		ui.El("p", ui.Attrs{"data-shell-screen": ""}, ui.Text(screenText(params["rest"]))),
		ui.El("p", nil, ui.Text("Local Go count: "), ui.El("output", ui.Attrs{"data-shell-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial)))),
		ui.El("button", ui.Attrs{"type": "button", "data-shell-increment": ""}, ui.Text("Increment in Go")),
		ui.El("button", ui.Attrs{"type": "button", "data-shell-replace": ""}, ui.Text("Replace with a different public note")),
		ui.El("p", ui.Attrs{"data-shell-navigation-error": "", "role": "status"}),
		ui.El("nav", ui.Attrs{"aria-label": "Go workspace routes"},
			link("Café note", "/workspace/notes/caf%C3%A9?tag=one&tag=two#details", "note"), ui.Text(" · "),
			link("Team board", "/workspace/boards/team?sort=recent", "board"), ui.Text(" · "),
			link("Client missing screen", "/workspace/missing/record", "missing"), ui.Text(" · "),
			link("Workspace root", "/workspace", "root"), ui.Text(" · "),
			link("Home", "/", "home")),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": "#details", "data-shell-fragment-link": ""}, ui.Text("Show details fragment"))),
		ui.El("section", ui.Attrs{"id": "details"}, ui.El("h2", nil, ui.Text("Public shell details")),
			ui.El("p", nil, ui.Text("All shell bytes and props are public. The client missing-screen still has HTTP 200. Unknown URLs outside this prefix require a real static 404."))),
	))
	if err != nil {
		return err
	}
	count, err := view.Require("[data-shell-count]")
	if err != nil {
		return err
	}
	increment, err := view.Require("[data-shell-increment]")
	if err != nil {
		return err
	}
	replace, err := view.Require("[data-shell-replace]")
	if err != nil {
		return err
	}
	navigationError, err := view.Require("[data-shell-navigation-error]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	view.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	view.On(increment, "click", func(client.Event) { state.Set(state.Get() + 1) })
	view.On(replace, "click", func(client.Event) {
		if err := client.Replace(client.MustURL("/workspace/notes/replaced?tag=replace#details")); err != nil {
			navigationError.Text(err.Error())
		}
	})
	// Hash-only commits keep the view and counter. ObserveLocation is owned by
	// the ClientView's child scope and unsubscribes when this view is disposed.
	fields := make(map[string]client.Element, 4)
	for _, name := range []string{"pathname", "rest", "query", "hash"} {
		field, err := view.Require("[data-shell-" + name + "]")
		if err != nil {
			return err
		}
		fields[name] = field
	}
	_, err = view.ObserveLocation(func(location client.Location) {
		params, matched := client.CurrentRouteParams("/workspace/*rest?")
		if !matched {
			return
		}
		fields["pathname"].Text(location.Pathname)
		fields["rest"].Text(params["rest"])
		fields["query"].Text(queryText(location))
		fields["hash"].Text(fragmentText(location.Hash))
	})
	return err
}

func field(label, name, value string) ui.Node {
	return ui.Fragment(ui.El("dt", nil, ui.Text(label)), ui.El("dd", ui.Attrs{"data-shell-" + name: ""}, ui.Text(value)))
}

func link(label, target, name string) ui.Node {
	return ui.El("a", ui.Attrs{"href": client.MustURL(target), "data-shell-link": name}, ui.Text(label))
}

func queryText(location client.Location) string {
	data, _ := json.Marshal(location.Query())
	return string(data)
}

func fragmentText(hash string) string {
	value := strings.TrimPrefix(hash, "#")
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func screenText(rest string) string {
	if rest == "" || strings.HasPrefix(rest, "notes/") || rest == "boards/team" {
		return "Public workspace route: " + rest
	}
	return "Client missing screen: this public shell still has HTTP 200."
}
