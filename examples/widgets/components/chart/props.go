package chart

import "github.com/Suhaibinator/Next.go/scripts"

type Props struct {
	Mode       string          `json:"mode"`
	Dependency scripts.Script  `json:"dependency"`
	Module     *scripts.Module `json:"module,omitempty"`
}
