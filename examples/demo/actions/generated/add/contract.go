package add

import "github.com/Suhaibinator/Next.go/mutation"

type Input struct {
	Amount int64 `json:"amount"`
	Fail   bool  `json:"fail"`
}

type Output struct {
	Count    int64 `json:"count"`
	Revision int64 `json:"revision"`
}

// The schema revision is explicit: changing a Go type does not silently change
// the public wire contract used by a previously built browser.
var Contract = mutation.Must[Input, Output]("generated/add", "v1")
