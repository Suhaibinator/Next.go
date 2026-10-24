// Package mutations shares only public mutation DTOs and descriptors. Native
// handlers stay in app/; the browser never imports those implementations.
package mutations

import "github.com/Suhaibinator/Next.go/mutation"

type IncrementInput struct {
	Amount int64 `json:"amount"`
}
type IncrementOutput struct {
	Applied int64 `json:"applied"`
	Count   int64 `json:"count"`
}

var Increment = mutation.Must[IncrementInput, IncrementOutput]("optimistic-increment", "v1")
