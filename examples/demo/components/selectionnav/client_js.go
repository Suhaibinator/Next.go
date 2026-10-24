//go:build js && wasm

package selectionnav

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	updates, err := s.Require("[data-selection-updates]")
	if err != nil {
		return err
	}
	count := 0
	for i, item := range p.Items {
		if err := item.Selection.Validate(); err != nil {
			return err
		}
		output, err := s.Require(`[data-selection-index="` + strconv.Itoa(i) + `"]`)
		if err != nil {
			return err
		}
		if output.Value.Get("textContent").String() != Text(item.Selection, true) {
			return fmt.Errorf("selection markup mismatch")
		}
		_, err = s.ObserveSelection(item.Selection.Scope, func(value client.Selection, present bool) {
			count++
			updates.Text(strconv.Itoa(count))
			output.Text(Text(value, present))
		})
		if err != nil {
			return err
		}
	}
	return nil
}
