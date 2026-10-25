package login

import "github.com/Suhaibinator/Next.go/mutation"

type Input struct {
	Credential string `json:"credential"`
}

type Output struct {
	Role string `json:"role"`
}

var Contract = mutation.Must[Input, Output]("session/login", "v1")
