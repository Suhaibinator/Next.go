package fileform

import "github.com/Suhaibinator/Next.go/examples/demo/uploads"

type Props struct {
	Title, Prefix, Action, Target string
	Receipt                       uploads.Receipt
	JSON                          bool
	Local                         bool
}
