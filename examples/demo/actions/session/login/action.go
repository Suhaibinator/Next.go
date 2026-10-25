//go:build !js

package login

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/cookiesession"
	"github.com/Suhaibinator/Next.go/mutation"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

func invalidCredential() error {
	return &actions.ValidationError{Message: "Enter a configured demo credential.", Fields: map[string]string{"credential": "Use a configured credential between 32 and 512 characters."}}
}

func Handle(c *actions.Context, input Input) (mutation.Result[Output], error) {
	if len(input.Credential) < 32 || len(input.Credential) > 512 || strings.ContainsAny(input.Credential, " \t\r\n") {
		return mutation.Result[Output]{}, invalidCredential()
	}
	role, cookie, err := cookiesession.Login(c.Context, c.Request, input.Credential)
	if err != nil {
		return mutation.Result[Output]{}, err
	}
	return mutation.Result[Output]{Data: Output{Role: string(role)}, Cookies: []*http.Cookie{cookie}, Redirect: "/cookie-session"}, nil
}

func Options() mutation.BindOptions[Input] {
	return mutation.BindOptions[Input]{Authorize: func(c *actions.Context) error {
		if !cookiesession.Available(c.Context, c.Request) {
			return actions.ErrForbidden
		}
		return nil
	}, Form: func(_ *actions.Context, values url.Values) (Input, error) {
		if len(values["credential"]) != 1 {
			return Input{}, invalidCredential()
		}
		return Input{Credential: values.Get("credential")}, nil
	}}
}
