//go:build js && wasm

package linkhint

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"strconv"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	for i := range p.Links {
		link, err := s.Require(`[data-link-hint-link="` + strconv.Itoa(i) + `"]`)
		if err != nil {
			return err
		}
		hint, err := s.Require(`[data-link-hint="` + strconv.Itoa(i) + `"]`)
		if err != nil {
			return err
		}
		if _, err := s.ObserveLinkStatus(link, func(value client.LinkStatus) {
			link.Attribute("data-link-pending", strconv.FormatBool(value.Pending))
			text := ""
			if value.Pending {
				text = "Loading…"
			}
			hint.Text(text)
		}); err != nil {
			return err
		}
	}
	return nil
}
