//go:build js && wasm

package retentionpanel

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	if props.Fail {
		return fmt.Errorf("deliberate direct-owner mount failure")
	}
	find := func(selector string) client.Element {
		element, failure := scope.Require(selector)
		if failure != nil && err == nil {
			err = failure
		}
		return element
	}
	count := find("[data-retained-count]")
	increment := find("[data-retained-increment]")
	ticks := find("[data-retained-ticks]")
	setupOutput := find("[data-retained-setups]")
	cleanupOutput := find("[data-retained-cleanups]")
	taskButton := find("[data-retained-task]")
	completedOutput := find("[data-retained-task-completed]")
	canceledOutput := find("[data-retained-task-canceled]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(props.Initial) {
		return fmt.Errorf("retained panel did not receive its SSR count")
	}
	state := client.NewState(props.Initial)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	if props.RetireNavigate {
		// Terminal retirement may begin a newer navigation. A retained hide does
		// not replay this cleanup or start this request.
		scope.Cleanup(func() { _ = client.Navigate("/c?retired=1") })
	}
	scope.On(increment, "click", func(client.Event) { state.Set(state.Get() + 1) })
	if props.CleanupPanic {
		scope.Cleanup(func() { panic("deliberate old cleanup failure") })
	}
	var setups, cleanups, clock, completed, canceled int
	visible := false
	life, err := scope.Lifetime()
	if err != nil {
		return err
	}
	_, err = life.Effect(func() (func(), error) {
		visible = true
		setups++
		setupOutput.Text(strconv.Itoa(setups))
		if props.ResumeFail && setups > 1 {
			visible = false
			return nil, fmt.Errorf("deliberate retained effect resume failure")
		}
		cleanupOutput.Text(strconv.Itoa(cleanups))
		completedOutput.Text(strconv.Itoa(completed))
		canceledOutput.Text(strconv.Itoa(canceled))
		effect, cancel := context.WithCancel(context.Background())
		if err := scope.Go(func(ctx context.Context) {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-effect.Done():
					return
				case <-ticker.C:
					if ctx.Err() != nil || effect.Err() != nil {
						return
					}
					clock++
					ticks.Text(strconv.Itoa(clock))
				}
			}
		}); err != nil {
			cancel()
			return nil, err
		}
		return func() { visible = false; cancel(); cleanups++; cleanupOutput.Text(strconv.Itoa(cleanups)) }, nil
	})
	if err != nil {
		return err
	}
	scope.On(taskButton, "click", func(client.Event) {
		if failure := scope.Go(func(ctx context.Context) {
			response, failure := client.Fetch(ctx, client.MustURL("/api/task"), client.FetchOptions{MaxBytes: 1024, Cache: "no-store", Redirect: "error"})
			if ctx.Err() != nil {
				canceled++
				if visible {
					canceledOutput.Text(strconv.Itoa(canceled))
				}
				return
			}
			if failure != nil || response.StatusCode != 200 {
				canceled++
				canceledOutput.Text(strconv.Itoa(canceled))
				return
			}
			completed++
			completedOutput.Text(strconv.Itoa(completed))
		}); failure != nil {
			canceled++
		}
	})
	increment.Property("disabled", false)
	taskButton.Property("disabled", false)
	return nil
}
