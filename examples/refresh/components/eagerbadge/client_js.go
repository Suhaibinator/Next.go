//go:build js && wasm

package eagerbadge

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
)

const eagerProof = "eager-v1"

func Mount(scope *client.Scope, raw json.RawMessage) error {
	if _, err := client.DecodeProps[Props](raw); err != nil {
		return err
	}
	node, err := scope.Require("[data-refresh-eager]")
	if err != nil {
		return err
	}
	node.Text(eagerProof)
	return nil
}
