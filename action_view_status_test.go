package nextgo

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestActionViewCustomNotFoundRetainsConfirmedResult(t *testing.T) {
	var count atomic.Int64
	app := testApp(t, Config{NotFound: func(c *Context) (Page, error) {
		return Page{Title: "Still missing", Body: ui.Text(fmt.Sprintf("Missing %s after %d", c.Query.Get("name"), count.Load()))}, nil
	}, Routes: []Route{{Pattern: "/", Page: static("home")}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		return actions.Result{Data: count.Add(1)}, nil
	})}})
	defer app.Close(context.Background())
	frames := actionViewTestFrames(t, actionViewTestServe(app, actionViewTestRequest(app, "/missing?name=route")))
	page := actionViewTestPage(t, frames)
	if count.Load() != 1 || string(frames[0].Result) != `{"data":1}` || page.Status != 404 || page.Title != "Still missing" || page.HTML != "Missing route after 1" {
		t.Fatal("custom not-found action refresh lost confirmation or current status", count.Load(), string(frames[0].Result), page)
	}
}
