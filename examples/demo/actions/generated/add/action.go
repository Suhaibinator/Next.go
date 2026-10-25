//go:build !js

package add

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/actionstate"
	"github.com/Suhaibinator/Next.go/mutation"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

func invalidAmount() error {
	return &actions.ValidationError{Message: "Choose an amount from 1 to 5.", Fields: map[string]string{"amount": "Enter a whole number from 1 to 5."}}
}

// Handle is intentionally public and changes only ephemeral demonstration data.
// Production mutations must authorize their input and use durable storage.
func Handle(c *actions.Context, input Input) (mutation.Result[Output], error) {
	if input.Amount < 1 || input.Amount > 5 {
		return mutation.Result[Output]{}, invalidAmount()
	}
	if input.Fail {
		return mutation.Result[Output]{}, fmt.Errorf("private generated demo storage detail")
	}
	value, err := actionstate.Add(c.Context, input.Amount)
	if err != nil {
		return mutation.Result[Output]{}, err
	}
	return mutation.Result[Output]{Data: Output{Count: value.Count, Revision: value.Revision}, Redirect: "/generated-actions", RevalidateTags: []string{"generated-actions-demo"}, RevalidatePaths: []string{"/generated-actions"}}, nil
}

func Options() mutation.BindOptions[Input] {
	return mutation.BindOptions[Input]{Form: func(_ *actions.Context, form url.Values) (Input, error) {
		if len(form["amount"]) != 1 || len(form["fail"]) > 1 {
			return Input{}, invalidAmount()
		}
		amount, err := strconv.ParseInt(form.Get("amount"), 10, 64)
		if err != nil {
			return Input{}, invalidAmount()
		}
		var fail bool
		if raw := form.Get("fail"); raw != "" {
			fail, err = strconv.ParseBool(raw)
			if err != nil {
				return Input{}, &actions.ValidationError{Message: "Invalid demo failure selection."}
			}
		}
		return Input{Amount: amount, Fail: fail}, nil
	}}
}
