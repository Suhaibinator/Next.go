//go:build !js

package save

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/actionviewstate"
	"github.com/Suhaibinator/Next.go/mutation"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

func invalidAmount() error {
	return &actions.ValidationError{Message: "Choose an amount from 1 to 5.", Fields: map[string]string{"amount": "Enter a whole number from 1 to 5."}}
}

// This intentionally public demonstration counter is process-local. Application
// handlers must authorize callers and commit durable data before returning.
func Handle(c *actions.Context, input Input) (mutation.Result[Output], error) {
	if input.Amount < 1 || input.Amount > 5 {
		return mutation.Result[Output]{}, invalidAmount()
	}
	if input.Fail {
		return mutation.Result[Output]{}, fmt.Errorf("private action view storage diagnostic")
	}
	value, err := actionviewstate.Add(c.Context, input.Amount, input.ViewFail)
	if err != nil {
		return mutation.Result[Output]{}, err
	}
	return mutation.Result[Output]{Data: Output{Count: value.Count, Revision: value.Revision}, RevalidateTags: []string{"action-view-demo"}, RevalidatePaths: []string{"/action-view"}}, nil
}

func Options() mutation.BindOptions[Input] {
	return mutation.BindOptions[Input]{Form: func(_ *actions.Context, form url.Values) (Input, error) {
		if len(form["amount"]) != 1 || len(form["fail"]) > 1 || len(form["viewFail"]) > 1 {
			return Input{}, invalidAmount()
		}
		amount, err := strconv.ParseInt(form.Get("amount"), 10, 64)
		if err != nil {
			return Input{}, invalidAmount()
		}
		parseFlag := func(name string) (bool, error) {
			if form.Get(name) == "" {
				return false, nil
			}
			return strconv.ParseBool(form.Get(name))
		}
		fail, err := parseFlag("fail")
		if err != nil {
			return Input{}, &actions.ValidationError{Message: "Invalid demo failure selection."}
		}
		viewFail, err := parseFlag("viewFail")
		if err != nil {
			return Input{}, &actions.ValidationError{Message: "Invalid demo view selection."}
		}
		return Input{Amount: amount, Fail: fail, ViewFail: viewFail}, nil
	}}
}
