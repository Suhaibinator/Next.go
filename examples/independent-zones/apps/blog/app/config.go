//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/mutation"
	"independent.example/blog/internal/state"
	"independent.example/blog/mutations"
	"net/url"
	"strconv"
)

func Configure(c *nextgo.Config) {
	// The release wrapper owns BasePath, AssetPrefix, runtime and public origin.
	c.PreserveLayouts = true
	router := actions.New(actions.Options{MaxBodyBytes: 2048})
	mutations.Preview.Bind(router, func(ctx *actions.Context, input mutations.Input) (mutation.Result[mutations.Output], error) {
		if input.Amount < 1 || input.Amount > 5 {
			return mutation.Result[mutations.Output]{}, &actions.ValidationError{Message: "Choose an amount from 1 to 5.", Fields: map[string]string{"amount": "Use a whole number from 1 to 5."}}
		}
		if err := ctx.Err(); err != nil {
			return mutation.Result[mutations.Output]{}, err
		}
		info := nextgo.CurrentZone(ctx)
		revision := state.Apply(input.Amount)
		return mutation.Result[mutations.Output]{Data: mutations.Output{Zone: info.ID, NativeVersion: state.NativeVersion, Instance: state.Instance(), Revision: revision, Applied: input.Amount}, Redirect: "/?preview=1"}, nil
	}, mutation.BindOptions[mutations.Input]{Form: func(_ *actions.Context, values url.Values) (mutations.Input, error) {
		if len(values) != 1 || len(values["amount"]) != 1 {
			return mutations.Input{}, &actions.ValidationError{Message: "Submit exactly one amount."}
		}
		amount, err := strconv.ParseInt(values.Get("amount"), 10, 64)
		if err != nil {
			return mutations.Input{}, &actions.ValidationError{Message: "Use a whole number."}
		}
		return mutations.Input{Amount: amount}, nil
	}})
	c.Actions = router
}
