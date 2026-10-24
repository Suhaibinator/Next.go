package lazycounter

import "github.com/Suhaibinator/Next.go/scripts"

type Props struct {
	Dependency *scripts.Script `json:"dependency,omitempty"`
	Label      string          `json:"label"`
	Initial    int             `json:"initial"`
	Target     string          `json:"target"`
}
