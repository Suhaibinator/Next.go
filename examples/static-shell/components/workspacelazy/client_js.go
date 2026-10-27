//go:build js && wasm

package workspacelazy

import (
	"encoding/json"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/static-shell/components/workspace"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	return workspace.MountView(scope, raw, "workspacelazy")
}
