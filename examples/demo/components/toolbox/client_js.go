//go:build js && wasm

package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/demo/routes"
	"net/url"
	"strconv"
	"strings"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := s.Require("[data-server-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.FormatInt(p.Count, 10) {
		return fmt.Errorf("toolbox count does not match server props")
	}
	refresh, err := s.Require("[data-refresh]")
	if err != nil {
		return err
	}
	navigate, err := s.Require("[data-navigate]")
	if err != nil {
		return err
	}
	replace, err := s.Require("[data-replace]")
	if err != nil {
		return err
	}
	prefetch, err := s.Require("[data-prefetch]")
	if err != nil {
		return err
	}
	increment, err := s.Require("[data-action-increment]")
	if err != nil {
		return err
	}
	only, err := s.Require("[data-action-only]")
	if err != nil {
		return err
	}
	status, err := s.Require("[data-action-status]")
	if err != nil {
		return err
	}
	base, err := url.Parse(s.Root.Value.Get("ownerDocument").Get("baseURI").String())
	if err != nil {
		return err
	}
	endpoint := base.ResolveReference(&url.URL{Path: client.MustURL("/_nextgo/actions/increment")}).String()
	ctx, cancel := context.WithCancel(context.Background())
	s.Cleanup(cancel)
	s.On(refresh, "click", func(client.Event) { client.Refresh() })
	s.On(navigate, "click", func(client.Event) {
		if err := client.Navigate(client.MustURL(routes.Posts())); err != nil {
			status.Text(err.Error())
		}
	})
	guide, err := s.Require("[data-guide-path]")
	if err != nil {
		return err
	}
	openGuide, err := s.Require("[data-guide-navigate]")
	if err != nil {
		return err
	}
	openCatalog, err := s.Require("[data-catalog-navigate]")
	if err != nil {
		return err
	}
	startTyped := func(path string, err error) {
		if err == nil {
			path, err = client.URL(path)
		}
		if err == nil {
			err = client.Navigate(path)
		}
		if err != nil {
			status.Text("Invalid guide path: " + err.Error())
		}
	}
	s.On(openGuide, "click", func(client.Event) {
		startTyped(routes.DocsByPath(routes.DocsByPathParams{Path: strings.Split(guide.Value.Get("value").String(), "/")}))
	})
	s.On(openCatalog, "click", func(client.Event) { startTyped(routes.CatalogByPath(routes.CatalogByPathParams{})) })
	openGuide.Property("disabled", false)
	openCatalog.Property("disabled", false)
	s.On(replace, "click", func(client.Event) {
		target := replace.Value.Call("getAttribute", "data-route-target").String()
		if strings.HasPrefix(target, "/") {
			resolved, err := client.URL(target)
			if err != nil {
				status.Text(err.Error())
				return
			}
			target = resolved
		}
		if err := client.Replace(target); err != nil {
			status.Text(err.Error())
		}
	})
	s.On(prefetch, "click", func(client.Event) {
		if err := client.Prefetch(client.MustURL("/posts")); err != nil {
			status.Text(err.Error())
		} else {
			status.Text("Prefetch requested. Public responses may be reused.")
		}
	})
	save := func(button client.Element, refreshPage bool) {
		button.Property("disabled", true)
		status.Text("Saving on the server…")
		go func() {
			err := incrementServer(ctx, endpoint)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				status.Text(err.Error())
				button.Property("disabled", false)
				return
			}
			if refreshPage {
				status.Text("Saved. Refreshing server data…")
				client.Refresh()
			} else {
				button.Property("disabled", false)
				status.Text("Saved without refreshing. Navigate to read the published value.")
			}
		}()
	}
	s.On(increment, "click", func(client.Event) { save(increment, true) })
	s.On(only, "click", func(client.Event) { save(only, false) })
	for _, button := range []client.Element{refresh, navigate, increment, only, replace, prefetch} {
		button.Property("disabled", false)
	}
	status.Text("Ready. Mutations execute in Go on the server.")
	return nil
}

func incrementServer(ctx context.Context, endpoint string) error {
	response, err := client.Fetch(ctx, endpoint, client.FetchOptions{
		Method: "POST", Headers: map[string]string{"Content-Type": "application/json", "Accept": "application/json"},
		Body: []byte(`{"amount":1}`), MaxBytes: 64 << 10,
	})
	if err != nil {
		return fmt.Errorf("Unable to reach the server. Please try again.")
	}
	var data struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &data); err != nil {
		return fmt.Errorf("The server returned an invalid response.")
	}
	if response.StatusCode != 200 || data.Error != nil {
		return fmt.Errorf("The action could not be completed. Please try again.")
	}
	return nil
}
