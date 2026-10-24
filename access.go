package nextgo

import (
	"errors"
	"net/http"

	"github.com/Suhaibinator/Next.go/internal/accesscontrol"
	"github.com/Suhaibinator/Next.go/ui"
)

// Return these errors from a page or synchronous data loader before accessing
// protected data. They select denial UI; they do not authenticate requests.
var ErrUnauthorized = accesscontrol.Unauthorized
var ErrForbidden = accesscontrol.Forbidden

// AccessBoundary declares denial UI and its safe outer layout prefix. The
// generator discovers unauthorized.go/forbidden.go and sets Layouts to the
// number of wrappers strictly above that directory. Entries are outermost first.
type AccessBoundary struct {
	Unauthorized PageFunc
	Forbidden    PageFunc
	Layouts      int
}

type layoutError struct {
	index int
	cause error
}

func (e *layoutError) Error() string { return e.cause.Error() }
func (e *layoutError) Unwrap() error { return e.cause }

func accessStatus(cause error) int {
	if errors.Is(cause, ErrForbidden) {
		return http.StatusForbidden
	}
	if errors.Is(cause, ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	return 0
}

func (a *App) renderAccess(c *Context, route Route, cause error) (page Page, status int) {
	status = accessStatus(cause)
	// A failing layout cannot be used to render its own denial, nor can a
	// boundary below it run with an invalid or protected parent layout.
	limit := len(route.Layouts)
	var failed *layoutError
	if errors.As(cause, &failed) {
		limit = failed.index
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.config.Logger.Printf("access boundary panic (%T)", recovered)
			page, status = errorPage(), http.StatusInternalServerError
		}
	}()
	handler := a.config.Unauthorized
	if status == http.StatusForbidden {
		handler = a.config.Forbidden
	}
	prefix := 0
	for i := len(route.Access) - 1; i >= 0; i-- {
		boundary := route.Access[i]
		if boundary.Layouts > limit {
			continue
		}
		selected := boundary.Unauthorized
		if status == http.StatusForbidden {
			selected = boundary.Forbidden
		}
		if selected != nil {
			handler, prefix = selected, boundary.Layouts
			break
		}
	}
	if handler == nil {
		title, message := "Sign in required", "Authentication is required to view this page."
		if status == http.StatusForbidden {
			title, message = "Access denied", "You do not have permission to view this page."
		}
		return Page{Title: title, Body: ui.El("main", nil, ui.El("h1", nil, ui.Text(title)), ui.El("p", nil, ui.Text(message)))}, status
	}
	var err error
	page, err = handler(c)
	if err == nil {
		page.Body, err = a.wrapLayoutPrefix(c, route, page.Body, prefix)
	}
	if err != nil {
		a.config.Logger.Printf("access boundary failed (%T)", err)
		return errorPage(), http.StatusInternalServerError
	}
	page.Stream = false
	return page, status
}
