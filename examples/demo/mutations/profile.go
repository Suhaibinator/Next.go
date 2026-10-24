package mutations

import (
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
	"github.com/Suhaibinator/Next.go/mutation"
)

var SaveProfile = mutation.Must[profiles.Profile, profiles.Profile]("profile/save", "v1")
