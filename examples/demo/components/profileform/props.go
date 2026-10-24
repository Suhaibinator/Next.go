package profileform

import "github.com/Suhaibinator/Next.go/examples/demo/profiles"

type Props struct {
	Profile        profiles.Profile
	Action, Target string
	IDPrefix       string
	Mismatch       bool
	Local          bool
	PanicHandler   bool
	ValidateBlur   bool
}
