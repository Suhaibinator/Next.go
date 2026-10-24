//go:build !js

package app

import (
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/choices"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/mutation"
	"net/url"
)

func configureChoices(router *actions.Router) {
	mutations.PreviewChoices.Bind(router, func(c *actions.Context, input choices.Preferences) (mutation.Result[choices.Preferences], error) {
		if err := c.Context.Err(); err != nil {
			return mutation.Result[choices.Preferences]{}, err
		}
		if fields := choices.Validate(input); len(fields) > 0 {
			return mutation.Result[choices.Preferences]{}, &actions.ValidationError{Message: "Review your preview choices.", Fields: fields}
		}
		// Canonical ordering is the server's application policy, not persistence.
		output := choices.Preferences{Channel: input.Channel, Topics: []string{}}
		for _, topic := range []string{"routing", "ssr", "wasm"} {
			for _, v := range input.Topics {
				if v == topic {
					output.Topics = append(output.Topics, topic)
				}
			}
		}
		query := url.Values{"channel": {output.Channel}, "topics": output.Topics, "applied": {"1"}}
		return mutation.Result[choices.Preferences]{Data: output, Redirect: "/choices?" + query.Encode()}, nil
	}, mutation.BindOptions[choices.Preferences]{Form: func(c *actions.Context, values url.Values) (choices.Preferences, error) {
		if c.Request.MultipartForm != nil && len(c.Request.MultipartForm.File) > 0 {
			return choices.Preferences{}, &actions.ValidationError{Message: "This preview does not accept files."}
		}
		for name, items := range values {
			if (name != "channel" && name != "topics" && name != "intent") || (name != "topics" && len(items) != 1) || (name == "topics" && len(items) > 3) {
				return choices.Preferences{}, &actions.ValidationError{Message: "Unexpected or repeated preview field."}
			}
		}
		if intent := values.Get("intent"); intent != "" && intent != "preview" {
			return choices.Preferences{}, &actions.ValidationError{Message: "Invalid preview intent."}
		}
		return choices.Preferences{Channel: values.Get("channel"), Topics: append([]string(nil), values["topics"]...)}, nil
	}})
}
