//go:build js && wasm

package lazyordinary

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
	"strconv"
	"sync/atomic"
)

var mounts atomic.Uint64

func Mount(scope *client.Scope, raw json.RawMessage) error {
	source, err := client.Props(scope, raw, client.PropsOptions[Props]{Clone: func(value Props) Props { return value }})
	if err != nil {
		return err
	}
	initial, err := source.GetSnapshot()
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-lazy-count]")
	if err != nil {
		return err
	}
	increment, err := scope.Require("[data-lazy-increment]")
	if err != nil {
		return err
	}
	readButton, err := scope.Require("[data-lazy-read]")
	if err != nil {
		return err
	}
	query, err := scope.Require("[data-lazy-query]")
	if err != nil {
		return err
	}
	revision, err := scope.Require("[data-lazy-revision]")
	if err != nil {
		return err
	}
	location, err := scope.Require("[data-lazy-location]")
	if err != nil {
		return err
	}
	mount, err := scope.Require("[data-lazy-mount]")
	if err != nil {
		return err
	}
	state := client.NewState(initial.Value.Initial)
	count.Text(strconv.Itoa(state.Get()))
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(increment, "click", func(client.Event) { state.Set(state.Get() + 1) })
	read := func() error {
		snapshot, failure := source.GetSnapshot()
		if failure != nil {
			return failure
		}
		query.Text(snapshot.Value.Query)
		revision.Text(strconv.FormatUint(snapshot.Revision, 10))
		location.Text(snapshot.Location.Search)
		return nil
	}
	if err := read(); err != nil {
		return err
	}
	scope.On(readButton, "click", func(client.Event) {
		if failure := read(); failure != nil {
			revision.Text("stale")
		}
	})
	mount.Text(strconv.FormatUint(mounts.Add(1), 10))
	increment.Property("disabled", false)
	readButton.Property("disabled", false)
	return nil
}
