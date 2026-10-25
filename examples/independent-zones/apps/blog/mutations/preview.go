package mutations

import "github.com/Suhaibinator/Next.go/mutation"

type Input struct {
	Amount int64 `json:"amount"`
}
type Output struct {
	Zone          string `json:"zone"`
	NativeVersion string `json:"nativeVersion"`
	Instance      string `json:"instance"`
	Revision      int64  `json:"revision"`
	Applied       int64  `json:"applied"`
}

var Preview = mutation.Must[Input, Output]("preview", "v1")
