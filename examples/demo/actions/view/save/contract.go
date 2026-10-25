package save

import "github.com/Suhaibinator/Next.go/mutation"

type Input struct {
	Amount   int64 `json:"amount"`
	Fail     bool  `json:"fail"`
	ViewFail bool  `json:"viewFail"`
}

type Output struct {
	Count    int64 `json:"count"`
	Revision int64 `json:"revision"`
}

var Contract = mutation.Must[Input, Output]("view/save", "v1")
