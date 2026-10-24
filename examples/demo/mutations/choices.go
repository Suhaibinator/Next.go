package mutations

import (
	"github.com/Suhaibinator/Next.go/examples/demo/choices"
	"github.com/Suhaibinator/Next.go/mutation"
)

var PreviewChoices = mutation.Must[choices.Preferences, choices.Preferences]("choices/preview", "v1")
