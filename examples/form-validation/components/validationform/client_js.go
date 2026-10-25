//go:build js && wasm

package validationform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"time"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	container, err := scope.Require("[data-fv-instance]")
	if err != nil {
		return err
	}
	form, err := client.NewScopedForm(scope, p.Values, client.FormOptions[model.Values]{Clone: model.Clone, Validate: func(v model.Values) (client.FieldErrors, error) { return client.FieldErrors(model.Validate(v)), nil }})
	if err != nil {
		return err
	}
	statusNodes := map[string]client.Element{}
	for _, name := range []string{"name", "code"} {
		input, err := scope.Require(`[data-fv-field="` + name + `"]`)
		if err != nil {
			return err
		}
		errorNode, err := scope.Require(`[data-fv-error="` + name + `"]`)
		if err != nil {
			return err
		}
		status, err := scope.Require(`[data-fv-validation="` + name + `"]`)
		if err != nil {
			return err
		}
		statusNodes[name] = status
		get := func(v model.Values) string {
			if name == "name" {
				return v.Name
			}
			return v.Code
		}
		set := func(v *model.Values, value string) {
			if name == "name" {
				v.Name = value
			} else {
				v.Code = value
			}
		}
		if err := client.BindText(scope, input, form, name, get, set, client.FieldBindingOptions{Error: errorNode, ValidateOnBlur: true}); err != nil {
			return err
		}
	}
	empty, err := scope.Require(`[data-fv-radio="empty"]`)
	if err != nil {
		return err
	}
	email, err := scope.Require(`[data-fv-radio="email"]`)
	if err != nil {
		return err
	}
	channelError, err := scope.Require(`[data-fv-error="channel"]`)
	if err != nil {
		return err
	}
	if err := client.BindRadioChoice(scope, []client.Element{empty, email}, form, "channel", func(v model.Values) client.RadioChoice { return v.Channel }, func(v *model.Values, value client.RadioChoice) { v.Channel = value }, client.FieldBindingOptions{Error: channelError, ValidateOnBlur: true}); err != nil {
		return err
	}
	topics, err := scope.Require("[data-fv-topics]")
	if err != nil {
		return err
	}
	topicsError, err := scope.Require(`[data-fv-error="topics"]`)
	if err != nil {
		return err
	}
	if err := client.BindMultipleOptions(scope, topics, form, "topics", func(v model.Values) []int { return v.Topics }, func(v *model.Values, value []int) { v.Topics = value }, client.FieldBindingOptions{Error: topicsError, ValidateOnBlur: true}); err != nil {
		return err
	}
	root, err := scope.Require("[data-fv-form]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-fv-status]")
	if err != nil {
		return err
	}
	if err := client.BindSubmit(scope, root, form, func(ctx context.Context, input model.Values) (client.FormResult[model.Values], error) {
		data, err := json.Marshal(input)
		if err != nil {
			return client.FormResult[model.Values]{}, err
		}
		response, err := client.Fetch(ctx, p.Action, client.FetchOptions{Method: "POST", Headers: map[string]string{"Content-Type": "application/json", "Accept": "application/json"}, Body: data, MaxBytes: 4096, Cache: "no-store", Redirect: "error"})
		if err != nil {
			return client.FormResult[model.Values]{}, err
		}
		var result model.PreviewResult
		if response.StatusCode != 200 && response.StatusCode != 422 {
			return client.FormResult[model.Values]{}, fmt.Errorf("public preview unavailable")
		}
		if err := json.Unmarshal(response.Body, &result); err != nil {
			return client.FormResult[model.Values]{}, err
		}
		if response.StatusCode == 422 {
			return client.FormResult[model.Values]{}, &client.FormError{Message: result.Message, Fields: client.FieldErrors(result.Fields)}
		}
		return client.FormResult[model.Values]{Value: result.Value, Message: result.Message}, nil
	}, client.SubmitOptions{Status: status, FailureMessage: "Preview unavailable. Nothing was saved; retry explicitly."}); err != nil {
		return err
	}
	for _, name := range []string{"name", "code"} {
		debounce := 250 * time.Millisecond
		if name == "code" {
			debounce = 350 * time.Millisecond
		}
		controller, err := client.BindFieldValidation(scope, form, name, func(ctx context.Context, input model.Values) (string, error) {
			value := input.Name
			if name == "code" {
				value = input.Code
			}
			target := p.ValidateURL + "?" + url.Values{"field": {name}, "value": {value}}.Encode()
			body, err := client.ResourceGET(target, 2048, 0)(ctx)
			if err != nil {
				return "", err
			}
			var result model.ValidationResult
			if err := json.Unmarshal(body, &result); err != nil {
				return "", err
			}
			return result.Message, nil
		}, client.FieldValidationOptions{OnInput: true, OnBlur: true, Debounce: debounce, Timeout: 10 * time.Second, OnError: func(error) {
			node := statusNodes[name]
			node.Attribute("data-state", "error")
			node.Text("Check unavailable; retry explicitly.")
		}})
		if err != nil {
			return err
		}
		for _, kind := range []string{"check", "retry"} {
			button, err := scope.Require(`[data-fv-` + kind + `="` + name + `"]`)
			if err != nil {
				return err
			}
			scope.On(button, "click", func(client.Event) { _ = controller.Trigger() })
			button.Property("disabled", false)
		}
	}
	clear, err := scope.Require("[data-fv-clear-radio]")
	if err != nil {
		return err
	}
	scope.On(clear, "click", func(client.Event) {
		if form.Capture() == nil {
			_ = form.Edit("channel", func(v *model.Values) { v.Channel = client.RadioChoice{} })
		}
	})
	clear.Property("disabled", false)
	localValidate, err := scope.Require("[data-fv-local-validate]")
	if err != nil {
		return err
	}
	scope.On(localValidate, "click", func(client.Event) {
		if form.Capture() == nil {
			_, _ = form.Validate()
		}
	})
	localValidate.Property("disabled", false)
	channelState, err := scope.Require("[data-fv-channel-state]")
	if err != nil {
		return err
	}
	indices, err := scope.Require("[data-fv-option-indices]")
	if err != nil {
		return err
	}
	values, err := scope.Require("[data-fv-option-values]")
	if err != nil {
		return err
	}
	confirmed, err := scope.Require("[data-fv-confirmed-indices]")
	if err != nil {
		return err
	}
	type pendingState struct {
		active bool
		value  model.Values
		reset  uint64
	}
	checks := map[string]pendingState{}
	previousValues := model.Clone(p.Values)
	stop, err := form.Observe(func(snapshot client.FormSnapshot[model.Values]) {
		draftChanged := !reflect.DeepEqual(previousValues, snapshot.Values)
		channelState.Text(model.ChannelSummary(snapshot.Values.Channel))
		selected, _ := json.Marshal(snapshot.Values.Topics)
		indices.Text(string(selected))
		selectedValues, _ := model.SelectedValues(snapshot.Values.Topics)
		encoded, _ := json.Marshal(selectedValues)
		values.Text(string(encoded))
		if snapshot.Phase == client.FormSucceeded {
			encoded, _ := json.Marshal(snapshot.Baseline.Topics)
			confirmed.Text(string(encoded))
		}
		pending := false
		for _, name := range []string{"name", "code"} {
			node := statusNodes[name]
			old := checks[name]
			if snapshot.Validating[name] {
				checks[name] = pendingState{active: true, value: model.Clone(snapshot.Values), reset: snapshot.ResetVersion}
				node.Attribute("data-state", "pending")
				node.Text("Checking public " + name + "…")
				pending = true
				continue
			}
			if snapshot.Errors[name] != "" {
				node.Attribute("data-state", "invalid")
				node.Text("Review this public field.")
			} else if old.active && old.reset == snapshot.ResetVersion && reflect.DeepEqual(old.value, snapshot.Values) {
				node.Attribute("data-state", "valid")
				node.Text("Public check passed.")
			} else if old.active || old.reset != snapshot.ResetVersion || draftChanged {
				node.Attribute("data-state", "idle")
				node.Text("Not checked.")
			}
			checks[name] = pendingState{reset: snapshot.ResetVersion}
		}
		if pending {
			status.Text("Checking public fields; wait before preview.")
		}
		previousValues = model.Clone(snapshot.Values)
	})
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	scope.Cleanup(func() { scope.Root.Attribute("data-fv-closed", "true"); container.Attribute("data-fv-closed", "true") })
	return nil
}
