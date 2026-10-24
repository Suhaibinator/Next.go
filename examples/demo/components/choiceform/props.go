package choiceform

import "github.com/Suhaibinator/Next.go/examples/demo/choices"

type Props struct {
	Values                 choices.Preferences
	Prefix, Action, Target string
	Local                  bool
}
