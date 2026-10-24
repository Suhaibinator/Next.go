//go:build js && wasm

package profileform

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
	"github.com/Suhaibinator/Next.go/mutation"
	"strconv"
	"strings"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	model, err := client.NewScopedForm(scope, p.Profile, client.FormOptions[profiles.Profile]{Validate: func(p profiles.Profile) (client.FieldErrors, error) {
		return client.FieldErrors(profiles.Validate(p)), nil
	}, Merge: profiles.Merge})
	if err != nil {
		return err
	}
	root, err := scope.Require("[data-profile-form]")
	if err != nil {
		return err
	}
	for _, name := range []string{"id", "version", "name", "bio", "email", "role"} {
		input, err := scope.Require(`[data-field="` + name + `"]`)
		if err != nil {
			return err
		}
		getter := func(p profiles.Profile) string {
			switch name {
			case "id":
				return p.ID
			case "version":
				return p.Version
			case "name":
				return p.Name
			case "bio":
				return p.Bio
			case "email":
				return p.Email
			default:
				return p.Role
			}
		}
		setter := func(p *profiles.Profile, value string) {
			switch name {
			case "id":
				p.ID = value
			case "version":
				p.Version = value
			case "name":
				p.Name = value
			case "bio":
				p.Bio = value
			case "email":
				p.Email = value
			case "role":
				p.Role = value
			}
		}
		opts := client.FieldBindingOptions{ValidateOnBlur: p.ValidateBlur}
		if name == "name" || name == "bio" || name == "email" || name == "role" {
			opts.Error, err = scope.Require(`[data-error="` + name + `"]`)
			if err != nil {
				return err
			}
		}
		if err := client.BindText(scope, input, model, name, getter, setter, opts); err != nil {
			return err
		}
	}
	newsletter, err := scope.Require(`[data-field="newsletter"]`)
	if err != nil {
		return err
	}
	if err := client.BindChecked(scope, newsletter, model, "newsletter", func(p profiles.Profile) bool { return p.Newsletter }, func(p *profiles.Profile, value bool) { p.Newsletter = value }, client.FieldBindingOptions{ValidateOnBlur: p.ValidateBlur}); err != nil {
		return err
	}
	status, err := scope.Require("[data-form-status]")
	if err != nil {
		return err
	}
	if !p.Local {
		if err := client.BindSubmit(scope, root, model, func(ctx context.Context, input profiles.Profile) (client.FormResult[profiles.Profile], error) {
			if p.PanicHandler {
				panic("synthetic form handler failure")
			}
			response, err := mutations.SaveProfile.Call(ctx, input)
			if err != nil {
				return profileResultError(err)
			}
			return client.FormResult[profiles.Profile]{Value: response.Data, Message: "Profile saved."}, nil
		}, client.SubmitOptions{Status: status}); err != nil {
			return err
		}
	} else {
		save, err := scope.Require("[data-local-save]")
		if err != nil {
			return err
		}
		reset, err := scope.Require("[data-reset-form]")
		if err != nil {
			return err
		}
		scope.On(save, "click", func(client.Event) {
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
			version, _ := strconv.ParseUint(input.Version, 10, 64)
			input.Version = strconv.FormatUint(version+1, 10)
			input.Name = strings.TrimSpace(input.Name)
			input.Email = strings.ToLower(strings.TrimSpace(input.Email))
			_ = ticket.Accept(input, "Local preview updated.")
		})
		scope.On(reset, "click", func(client.Event) { _ = model.Reset() })
		save.Property("disabled", false)
		reset.Property("disabled", false)
	}
	cancel, err := scope.Require("[data-cancel-form]")
	if err != nil {
		return err
	}
	dirty, err := scope.Require("[data-form-dirty]")
	if err != nil {
		return err
	}
	refresh, err := scope.Require("[data-refresh-form]")
	if err != nil {
		return err
	}
	stop, err := model.Observe(func(state client.FormSnapshot[profiles.Profile]) {
		if !scope.Root.Value.Get("isConnected").Bool() {
			return
		}
		if p.Local {
			text := "Ready."
			if state.Dirty {
				text = "Unsaved changes."
			}
			if state.Phase == client.FormFailed {
				text = "Review the highlighted fields."
			}
			if state.Phase == client.FormSucceeded {
				text = state.Message
			}
			status.Text(text)
		}
		cancel.Property("disabled", !state.Pending)
		text := "Saved values"
		if state.Dirty {
			text = "Unsaved changes"
		}
		dirty.Text(text)
		scope.Root.Attribute("data-form-version", state.Values.Version)
		scope.Root.Attribute("data-model-version", strconv.FormatUint(state.Version, 10))
	})
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	scope.On(cancel, "click", func(client.Event) { model.Cancel() })
	scope.On(refresh, "click", func(client.Event) { client.Refresh() })
	refresh.Property("disabled", false)
	scope.Cleanup(func() { scope.Root.Attribute("data-form-closed", "true") })
	return nil
}
func profileResultError(err error) (client.FormResult[profiles.Profile], error) {
	var failure *mutation.Error
	if errors.As(err, &failure) && failure != nil && failure.Status == 422 && failure.Code == "validation_error" {
		return client.FormResult[profiles.Profile]{}, &client.FormError{Message: failure.Message, Fields: client.FieldErrors(failure.Fields)}
	}
	return client.FormResult[profiles.Profile]{}, err
}
