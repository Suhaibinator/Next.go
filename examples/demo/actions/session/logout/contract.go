package logout

import "github.com/Suhaibinator/Next.go/mutation"

type Input struct{}

type Output struct {
	Role string `json:"role"`
}

var Contract = mutation.Must[Input, Output]("session/logout", "v1")
