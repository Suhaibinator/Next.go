//go:build js && wasm

package clientpanellazy

import (
	"encoding/json"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/demo/components/clientpanel"
)

func Mount(scope *client.Scope, raw json.RawMessage) error { return clientpanel.Mount(scope, raw) }
