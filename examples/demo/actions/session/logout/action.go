//go:build !js

package logout

import (
	"net/http"
	"net/url"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/cookiesession"
	"github.com/Suhaibinator/Next.go/mutation"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

func Handle(c *actions.Context, _ Input) (mutation.Result[Output], error) {
	cookie, err := cookiesession.Clear(c.Context, c.Request)
	if err != nil {
		return mutation.Result[Output]{}, err
	}
	return mutation.Result[Output]{Data: Output{Role: "anonymous"}, Cookies: []*http.Cookie{cookie}, Redirect: "/cookie-session"}, nil
}

func Options() mutation.BindOptions[Input] {
	return mutation.BindOptions[Input]{Authorize: func(c *actions.Context) error {
		if !cookiesession.Available(c.Context, c.Request) {
			return actions.ErrForbidden
		}
		return nil
	}, Form: func(_ *actions.Context, _ url.Values) (Input, error) { return Input{}, nil }}
}
