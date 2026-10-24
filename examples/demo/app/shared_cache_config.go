//go:build !js

package app

import (
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/shareddata"
)

func configureSharedCache(router *actions.Router) {
	router.Handle("shared-cache-stale", func(c *actions.Context) (actions.Result, error) {
		values, err := c.ParseForm()
		if err != nil {
			return actions.Result{}, err
		}
		if len(values) != 0 || c.Request.MultipartForm != nil && len(c.Request.MultipartForm.File) != 0 {
			return actions.Result{}, &actions.ValidationError{Message: "No fields are expected."}
		}
		if err := shareddata.MarkStale(c.Context); err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Redirect: "/shared-cache?refresh=background"}, nil
	})
	// This public demo only expires generated public data. Applications must
	// authorize real writes and invalidate only after their database commit.
	router.Handle("shared-cache-expire", func(c *actions.Context) (actions.Result, error) {
		values, err := c.ParseForm()
		if err != nil {
			return actions.Result{}, err
		}
		if len(values) != 0 || c.Request.MultipartForm != nil && len(c.Request.MultipartForm.File) != 0 {
			return actions.Result{}, &actions.ValidationError{Message: "No fields are expected."}
		}
		if err := shareddata.Expire(c.Context); err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Redirect: "/shared-cache"}, nil
	})
}
