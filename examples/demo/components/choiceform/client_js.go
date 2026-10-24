//go:build js && wasm

package choiceform

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/demo/choices"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/mutation"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	model, err := client.NewScopedForm(scope, p.Values, client.FormOptions[choices.Preferences]{Clone: choices.Clone, Merge: choices.Merge, Validate: func(p choices.Preferences) (client.FieldErrors, error) {
		return client.FieldErrors(choices.Validate(p)), nil
	}})
	if err != nil {
		return err
	}
	radios := []client.Element{}
	for _, v := range []string{"email", "sms"} {
		node, err := scope.Require(`[data-channel="` + v + `"]`)
		if err != nil {
			return err
		}
		radios = append(radios, node)
	}
	radioError, err := scope.Require(`[data-choice-error="channel"]`)
	if err != nil {
		return err
	}
	if err := client.BindRadioGroup(scope, radios, model, "channel", func(p choices.Preferences) string { return p.Channel }, func(p *choices.Preferences, v string) { p.Channel = v }, client.FieldBindingOptions{Error: radioError, ValidateOnBlur: true}); err != nil {
		return err
	}
	topics, err := scope.Require("[data-topics]")
	if err != nil {
		return err
	}
	topicError, err := scope.Require(`[data-choice-error="topics"]`)
	if err != nil {
		return err
	}
	if err := client.BindMultiple(scope, topics, model, "topics", func(p choices.Preferences) []string { return p.Topics }, func(p *choices.Preferences, v []string) { p.Topics = v }, client.FieldBindingOptions{Error: topicError, ValidateOnBlur: true}); err != nil {
		return err
	}
	form, err := scope.Require("[data-choice-form]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-choice-status]")
	if err != nil {
		return err
	}
	if !p.Local {
		err = client.BindSubmit(scope, form, model, func(ctx context.Context, input choices.Preferences) (client.FormResult[choices.Preferences], error) {
			response, err := mutations.PreviewChoices.Call(ctx, input)
			if err != nil {
				var failure *mutation.Error
				if errors.As(err, &failure) && failure != nil && failure.Status == 422 && failure.Code == "validation_error" {
					return client.FormResult[choices.Preferences]{}, &client.FormError{Message: failure.Message, Fields: client.FieldErrors(failure.Fields)}
				}
				return client.FormResult[choices.Preferences]{}, err
			}
			return client.FormResult[choices.Preferences]{Value: response.Data, Message: "Server preview confirmed."}, nil
		}, client.SubmitOptions{Status: status})
		if err != nil {
			return err
		}
	} else {
		apply, err := scope.Require("[data-choice-apply]")
		if err != nil {
			return err
		}
		reset, err := scope.Require("[data-choice-reset]")
		if err != nil {
			return err
		}
		scope.On(form, "submit", func(event client.Event) { event.PreventDefault() })
		scope.On(apply, "click", func(client.Event) {
			if err := model.Capture(); err != nil {
				return
			}
			ticket, err := model.Begin(context.Background())
			if err != nil {
				return
			}
			input, err := ticket.Input()
			if err != nil {
				return
			}
			_ = ticket.Accept(input, "Local choices applied.")
		})
		scope.On(reset, "click", func(client.Event) { _ = model.Reset() })
		apply.Property("disabled", false)
		reset.Property("disabled", false)
	}
	summary, err := scope.Require("[data-choice-summary]")
	if err != nil {
		return err
	}
	stop, err := model.Observe(func(state client.FormSnapshot[choices.Preferences]) {
		summary.Text(state.Values.Channel + ": " + joinTopics(state.Values.Topics))
		if p.Local {
			message := "Ready."
			if state.Dirty {
				message = "Unsaved choices."
			}
			if state.Phase == client.FormFailed {
				message = "Review the highlighted fields."
			}
			if state.Phase == client.FormSucceeded {
				message = state.Message
			}
			status.Text(message)
		}
	})
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	return nil
}
