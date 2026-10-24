//go:build !js

package app

import (
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
	"github.com/Suhaibinator/Next.go/mutation"
	"net/url"
)

func configureProfileForm(router *actions.Router) {
	mutations.SaveProfile.Bind(router, func(c *actions.Context, input profiles.Profile) (mutation.Result[profiles.Profile], error) {
		saved, fields, err := profiles.Save(c.Context, input)
		if err != nil {
			return mutation.Result[profiles.Profile]{}, err
		}
		if len(fields) > 0 {
			return mutation.Result[profiles.Profile]{}, &actions.ValidationError{Message: "Check the submitted profile; reload if its version changed.", Fields: fields}
		}
		return mutation.Result[profiles.Profile]{Data: saved, Redirect: "/forms?id=" + url.QueryEscape(saved.ID) + "&saved=1"}, nil
	}, mutation.BindOptions[profiles.Profile]{Form: func(c *actions.Context, values url.Values) (profiles.Profile, error) {
		if c.Request.MultipartForm != nil && len(c.Request.MultipartForm.File) > 0 {
			return profiles.Profile{}, &actions.ValidationError{Message: "This profile form does not accept uploaded files."}
		}
		allowed := map[string]bool{"id": true, "version": true, "name": true, "bio": true, "email": true, "role": true, "newsletter": true, "intent": true}
		for name, items := range values {
			if !allowed[name] || len(items) != 1 {
				return profiles.Profile{}, &actions.ValidationError{Message: "Unexpected or repeated form field."}
			}
		}
		if value := values.Get("newsletter"); value != "" && value != "on" {
			return profiles.Profile{}, &actions.ValidationError{Message: "Invalid newsletter value."}
		}
		if value := values.Get("intent"); value != "" && value != "save" {
			return profiles.Profile{}, &actions.ValidationError{Message: "Invalid submit intent."}
		}
		return profiles.Profile{ID: values.Get("id"), Version: values.Get("version"), Name: values.Get("name"), Bio: values.Get("bio"), Email: values.Get("email"), Role: values.Get("role"), Newsletter: values.Get("newsletter") == "on"}, nil
	}})
}
