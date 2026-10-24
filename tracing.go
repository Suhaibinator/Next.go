package nextgo

import (
	"github.com/Suhaibinator/Next.go/telemetry"
	"net/http"
)

func (a *App) load(c *Context, route Route, w http.ResponseWriter) (page Page, status int) {
	if !telemetry.Active(c.Context) {
		return a.loadPage(c, route, w, nil)
	}
	original, request := c.Context, c.Request
	ctx, span := telemetry.Start(original, telemetry.Info{Name: "nextgo.load", Route: route.Pattern})
	c.Context, c.Request = ctx, request.WithContext(ctx)
	completed := false
	defer func() {
		c.Context, c.Request = original, request
		result := telemetry.Result{Status: status}
		if !completed {
			result.Status = 500
			result.Error = telemetry.ErrorAborted
		}
		if failure := recover(); failure != nil {
			result.Error = telemetry.ErrorPanic
			span.End(result)
			panic(failure)
		}
		span.End(result)
	}()
	page, status = a.loadPage(c, route, w, span)
	completed = true
	return
}
