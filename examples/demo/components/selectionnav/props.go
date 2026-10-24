package selectionnav

import (
	"github.com/Suhaibinator/Next.go/routeinfo"
	"strings"
)

type Item struct {
	Label     string              `json:"label"`
	Selection routeinfo.Selection `json:"selection"`
}
type Props struct {
	Label string `json:"label"`
	Items []Item `json:"items"`
}

func Text(value routeinfo.Selection, present bool) string {
	if !present {
		return "unavailable"
	}
	if len(value.Segments) == 0 {
		return "index"
	}
	return strings.Join(value.Segments, " / ")
}
