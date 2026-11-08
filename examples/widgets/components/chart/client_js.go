//go:build js && wasm

package chart

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/scripts"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	host, err := scope.Require("[data-widget-host]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-widget-status]")
	if err != nil {
		return err
	}
	stop, err := scope.Require("[data-widget-stop]")
	if err != nil {
		return err
	}
	start, err := scope.Require("[data-widget-start]")
	if err != nil {
		return err
	}
	var current *client.Widget
	var generation uint64
	begin := func(retry bool) {
		generation++
		id, previous := generation, current
		if previous != nil {
			previous.Close()
		}
		status.Text("Loading chart library")
		if err := scope.Go(func(ctx context.Context) {
			if previous != nil {
				select {
				case <-previous.Settled():
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil || id != generation {
				return
			}
			var dependencies []scripts.Script
			var modules []scripts.Module
			if p.Module != nil {
				modules = []scripts.Module{*p.Module}
			} else {
				dependencies = []scripts.Script{p.Dependency}
			}
			widget, err := scope.Widget(host, client.WidgetOptions{Scripts: dependencies, Modules: modules, RetryFailedScripts: retry,
				Mount: func(ctx context.Context, owned *client.Scope) error {
					status.Text("Preparing chart")
					if p.Mode == "late" {
						// Explicit failure fixture: represent an external constructor
						// which cannot cancel underlying work until it completes.
						time.Sleep(350 * time.Millisecond)
					} else if ctx.Err() != nil {
						return ctx.Err()
					}
					constructor := js.Global().Get("uPlot")
					if p.Module != nil {
						namespace, err := client.ImportModule(ctx, *p.Module)
						if err != nil {
							return err
						}
						constructor = namespace.Get("default")
						if namespace.Get("libraryVersion").String() != "1.6.32" {
							return fmt.Errorf("chart adapter version unavailable")
						}
						count := namespace.Call("recordMount").Int()
						if namespace.Get("mountCount").Int() != count {
							return fmt.Errorf("chart adapter live binding unavailable")
						}
						owned.Root.Attribute("data-module-mount", fmt.Sprint(count))
					}
					if constructor.Type() != js.TypeFunction {
						return fmt.Errorf("chart library API unavailable")
					}
					options := map[string]any{"width": 480, "height": 240, "scales": map[string]any{"x": map[string]any{"time": false}},
						"series": []any{map[string]any{}, map[string]any{"label": "Value", "stroke": "#1257a5"}}}
					data := []any{[]any{1, 2, 3, 4}, []any{2, 4, 3, 5}}
					plot := constructor.New(js.ValueOf(options), js.ValueOf(data), owned.Root.Value)
					owned.Cleanup(func() { plot.Call("destroy") })
					if p.Mode == "error" {
						return fmt.Errorf("intentional post-construction fixture failure")
					}
					return nil
				}, Ready: func(err error) {
					if id != generation {
						return
					}
					if err != nil {
						status.Text("Chart unavailable")
						return
					}
					status.Text("Chart ready")
				}})
			if err != nil {
				status.Text("Chart unavailable")
				return
			}
			if ctx.Err() != nil || id != generation {
				widget.Close()
				return
			}
			current = widget
		}); err != nil {
			status.Text("Chart unavailable")
		}
	}
	scope.On(stop, "click", func(client.Event) {
		generation++
		if current != nil {
			current.Close()
		}
		status.Text("Chart stopped")
	})
	scope.On(start, "click", func(client.Event) { begin(true) })
	stop.Property("disabled", false)
	start.Property("disabled", false)
	begin(false)
	return nil
}
