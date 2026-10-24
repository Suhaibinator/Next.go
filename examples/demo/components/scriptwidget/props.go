package scriptwidget

import "github.com/Suhaibinator/Next.go/scripts"

type Props struct {
	Label      string         `json:"label"`
	Dependency scripts.Script `json:"dependency"`
	Global     string         `json:"global"`
}
