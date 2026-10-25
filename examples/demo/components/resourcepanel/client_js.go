//go:build js && wasm

package resourcepanel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	nodes := map[string]client.Element{}
	for _, name := range []string{"primary", "secondary", "status", "reuse", "refresh", "fail", "retry"} {
		node, err := scope.Require("[data-resource-" + name + "]")
		if err != nil {
			return err
		}
		nodes[name] = node
	}
	initial := props.Initial.Message + " · " + props.Initial.Generated
	if nodes["primary"].Value.Get("textContent").String() != initial || nodes["secondary"].Value.Get("textContent").String() != initial {
		return fmt.Errorf("resource markup differs from server data")
	}
	store, err := scope.ResourceStore(client.ResourceStoreOptions{MaxSources: 1, MaxEntries: 1, MaxInFlight: 2, MaxWaiters: 4, MaxBytes: 4096, MaxValueBytes: 2048, Timeout: 3 * time.Second})
	if err != nil {
		return err
	}
	failNext := false
	normal := client.ResourceGET(props.Endpoint, 2048, 15*time.Second)
	failing := client.ResourceGET(props.Endpoint+"?fail=1", 2048, 15*time.Second)
	source, err := store.Source(client.ResourceSourceOptions{Key: props.Seed.Key, PublicTTL: 15 * time.Second, Load: func(ctx context.Context) ([]byte, error) {
		if failNext {
			failNext = false
			return failing(ctx)
		}
		return normal(ctx)
	}})
	if err != nil {
		return err
	}
	primary, err := scope.AsyncResource(source, client.AsyncResourceOptions{Seed: &props.Seed})
	if err != nil {
		return err
	}
	// The second view starts from the same explicit SSR value. It consumes the
	// same immutable registered source on the first user-requested load.
	secondary, err := scope.AsyncResource(source, client.AsyncResourceOptions{})
	if err != nil {
		return err
	}
	primaryView := func(snapshot client.ResourceSnapshot) {
		scope.Root.Attribute("aria-busy", fmt.Sprint(snapshot.Phase == client.ResourceLoading))
		switch snapshot.Phase {
		case client.ResourceReady:
			var data Data
			if json.Unmarshal(snapshot.Body, &data) != nil {
				nodes["status"].Text("The resource response could not be decoded.")
				return
			}
			nodes["primary"].Text(data.Message + " · " + data.Generated)
			if snapshot.Seeded {
				nodes["status"].Text("Adopted the server seed without fetching.")
			} else {
				nodes["status"].Text("Public resource ready. Two views share only this explicitly registered public source.")
			}
		case client.ResourceLoading:
			nodes["status"].Text("Loading public data…")
		case client.ResourceFailed:
			nodes["status"].Text("The server request failed. Retry explicitly to recover.")
		case client.ResourceInvalidated:
			nodes["primary"].Text("Waiting for an explicit reload")
			nodes["status"].Text("Resource invalidated. No automatic request was sent.")
		}
		nodes["retry"].Property("disabled", snapshot.Phase != client.ResourceFailed)
	}
	stop, err := primary.Subscribe(primaryView)
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	stop, err = secondary.Subscribe(func(snapshot client.ResourceSnapshot) {
		if snapshot.Phase == client.ResourceReady {
			var data Data
			if json.Unmarshal(snapshot.Body, &data) == nil {
				nodes["secondary"].Text(data.Message + " · " + data.Generated)
			}
		} else if snapshot.Phase == client.ResourceInvalidated {
			nodes["secondary"].Text("Waiting for an explicit reload")
		}
	})
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	loadBoth := func() { _ = primary.Load(); _ = secondary.Load() }
	scope.On(nodes["reuse"], "click", func(client.Event) { loadBoth() })
	scope.On(nodes["refresh"], "click", func(client.Event) { client.InvalidateResources(); loadBoth() })
	scope.On(nodes["fail"], "click", func(client.Event) { client.InvalidateResources(); failNext = true; loadBoth() })
	scope.On(nodes["retry"], "click", func(client.Event) { _ = primary.Retry(); _ = secondary.Retry() })
	for _, name := range []string{"reuse", "refresh", "fail"} {
		nodes[name].Property("disabled", false)
	}
	scope.Cleanup(func() { scope.Root.Attribute("data-resource-closed", "true") })
	return nil
}
