//go:build js && wasm

package panel

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"strconv"
)

var sources = map[string]*client.PropsSource[Props]{}
var mounts = map[string]int{}

func Mount(root *client.Scope, raw json.RawMessage) error {
	source, err := client.Props(root, raw, client.PropsOptions[Props]{Clone: func(p Props) Props { return p }, Validate: func(s client.PropsSnapshot[Props]) error {
		if s.Value.Name != "A" && s.Value.Name != "B" || s.Value.Price == "" || s.Value.Product == "" || s.Value.Reject {
			return fmt.Errorf("demonstration props rejected")
		}
		return nil
	}})
	if err != nil {
		return err
	}
	initial, err := source.GetSnapshot()
	if err != nil {
		return err
	}
	p := initial.Value
	child, err := root.ClientView(Body(p, true))
	if err != nil {
		return err
	}
	sources[p.Name] = source
	root.Cleanup(func() {
		if sources[p.Name] == source {
			delete(sources, p.Name)
		}
	})
	mounts[p.Name]++
	find := func(selector string) client.Element {
		node, failure := child.Require(selector)
		if failure != nil && err == nil {
			err = failure
		}
		return node
	}
	price, query, rawQuery := find("[data-merge-price]"), find("[data-merge-query]"), find("[data-merge-raw-query]")
	version, product := find("[data-merge-version]"), find("[data-merge-product]")
	count, revision, coherent := find("[data-merge-count]"), find("[data-merge-revision]"), find("[data-merge-coherent]")
	increment, refresh, mountCount := find("[data-merge-increment]"), find("[data-merge-refresh]"), find("[data-merge-mount]")
	if err != nil {
		return err
	}
	mountCount.Text(strconv.Itoa(mounts[p.Name]))
	counter := client.NewState(p.Initial)
	child.Cleanup(counter.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	child.On(increment, "click", func(client.Event) { counter.Set(counter.Get() + 1) })
	child.On(refresh, "click", func(client.Event) { client.Refresh() })
	life, err := child.Lifetime()
	if err != nil {
		return err
	}
	navigated := false
	_, err = life.Effect(func() (func(), error) {
		current, failure := source.GetSnapshot()
		if failure != nil {
			return nil, failure
		}
		if current.Value.EffectFail {
			return nil, fmt.Errorf("demonstration props effect failed")
		}
		price.Text(current.Value.Price)
		query.Text(current.Value.Query)
		rawQuery.Text(current.Value.RawQuery)
		version.Text(current.Value.Version)
		product.Text(current.Value.Product)
		revision.Text(strconv.FormatUint(current.Revision, 10))
		other := "A"
		if p.Name == "A" {
			other = "B"
		}
		sibling := sources[other]
		if sibling == nil {
			coherent.Text("awaiting sibling")
			return nil, nil
		}
		seen, failure := sibling.GetSnapshot()
		if failure != nil {
			coherent.Text("cold sibling")
			return nil, nil
		}
		location, ready := client.CurrentLocation()
		if !ready || current.Revision != seen.Revision || current.Location != seen.Location || current.Location != location {
			coherent.Text("TORN")
			return nil, fmt.Errorf("cross-root props frame was torn")
		}
		coherent.Text("coherent")
		if p.Name == "A" && current.Value.NavigateOnUpdate && !navigated && current.Revision > initial.Revision {
			navigated = true
			if failure := client.Navigate("?q=c"); failure != nil {
				return nil, failure
			}
		}
		return nil, nil
	}, source)
	return err
}
