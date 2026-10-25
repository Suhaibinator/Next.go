//go:build js && wasm

package lazylinkhint

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/demo/components/linkhint"
)

func Mount(s *client.Scope, raw json.RawMessage) error { return linkhint.Mount(s, raw) }
