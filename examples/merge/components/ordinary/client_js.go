//go:build js && wasm

package ordinary

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"strconv"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	var source *client.PropsSource[Props]
	if props.InitialSource {
		source, err = client.Props(scope, raw, client.PropsOptions[Props]{Clone: func(value Props) Props { return value }})
		if err != nil {
			return err
		}
	}
	count, err := scope.Require("[data-ordinary-count]")
	if err != nil {
		return err
	}
	button, err := scope.Require("[data-ordinary-increment]")
	if err != nil {
		return err
	}
	state := client.NewState(2)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(button, "click", func(client.Event) { state.Set(state.Get() + 1) })
	button.Property("disabled", false)
	if source != nil {
		probe, err := scope.Require("[data-ordinary-props-revision]")
		if err != nil {
			return err
		}
		location, err := scope.Require("[data-ordinary-props-location]")
		if err != nil {
			return err
		}
		readButton, err := scope.Require("[data-ordinary-read]")
		if err != nil {
			return err
		}
		refresh, err := scope.Require("[data-ordinary-refresh]")
		if err != nil {
			return err
		}
		read := func() error {
			snapshot, failure := source.GetSnapshot()
			if failure != nil {
				return failure
			}
			probe.Text(strconv.FormatUint(snapshot.Revision, 10))
			location.Text(snapshot.Location.Search)
			return nil
		}
		if err := read(); err != nil {
			return err
		}
		scope.On(readButton, "click", func(client.Event) {
			if failure := read(); failure != nil {
				probe.Text("stale")
			}
		})
		readButton.Property("disabled", false)
		scope.On(refresh, "click", func(client.Event) { client.Refresh() })
		refresh.Property("disabled", false)
	}
	// The /boot start document deliberately starts a real navigation only after
	// its ordinary counter is fully bound. No PropsSource/ClientView or yielding
	// callback is involved, and the arrive document has Boot false.
	if props.Boot {
		return client.Navigate("/boot?step=arrive")
	}
	return nil
}
