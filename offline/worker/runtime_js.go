//go:build js && wasm

package worker

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/offlinejson"
)

type browserBackend struct {
	bridge   js.Value
	mu       sync.Mutex
	sequence uint64
	pending  map[string]chan browserResult
}
type browserResult struct {
	raw json.RawMessage
	ok  bool
}

func (b *browserBackend) Call(op string, arg any) (json.RawMessage, error) {
	encoded, err := json.Marshal(arg)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	if len(b.pending) >= 128 || b.sequence >= 1<<53-1 {
		b.mu.Unlock()
		return nil, errors.New("worker: physical capacity")
	}
	b.sequence++
	token := strconv.FormatUint(b.sequence, 10)
	ch := make(chan browserResult, 1)
	b.pending[token] = ch
	b.mu.Unlock()
	b.bridge.Call("perform", token, op, string(encoded))
	result := <-ch
	if !result.ok {
		return nil, errors.New("worker: browser operation failed")
	}
	return result.raw, nil
}

type browserEvent struct{ kind, token, payload, source string }

var workerFunctions []js.Func

func installRuntime(bridge js.Value) bool {
	backend := &browserBackend{bridge: bridge, pending: map[string]chan browserResult{}}
	engine, err := NewEngine([]byte(bridge.Get("descriptorJSON").String()), backend)
	if err != nil {
		return false
	}
	queue := make(chan browserEvent, engine.QueueLimit())
	var workMu sync.Mutex
	inFlight := 0
	dispatch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 4 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString || args[2].Type() != js.TypeString || args[3].Type() != js.TypeString {
			return false
		}
		event := browserEvent{args[0].String(), args[1].String(), args[2].String(), args[3].String()}
		if len(event.payload) > 8192 || len(event.source) > 2048 {
			return false
		}
		workMu.Lock()
		defer workMu.Unlock()
		if inFlight >= engine.Limit() {
			return false
		}
		select {
		case queue <- event:
			inFlight++
			return true
		default:
			return false
		}
	})
	resume := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 3 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString || args[2].Type() != js.TypeBoolean {
			return nil
		}
		token := args[0].String()
		backend.mu.Lock()
		ch := backend.pending[token]
		delete(backend.pending, token)
		backend.mu.Unlock()
		if ch != nil {
			raw := args[1].String()
			if len(raw) > 256<<10 {
				ch <- browserResult{ok: false}
			} else {
				ch <- browserResult{raw: json.RawMessage(raw), ok: args[2].Bool()}
			}
		}
		return nil
	})
	workerFunctions = append(workerFunctions, dispatch, resume)
	go func() {
		for event := range queue {
			var reply any
			switch event.kind {
			case "fetch":
				var request Request
				if offlinejson.Decode([]byte(event.payload), &request) != nil {
					reply = Decision{Kind: "fallback"}
				} else {
					reply = engine.Fetch(request)
				}
			case "message":
				var command Command
				if offlinejson.Decode([]byte(event.payload), &command) != nil {
					reply = engine.baseStatus("")
				} else {
					reply = engine.Message(command, event.source)
				}
			case "install", "activate":
				err := engine.Lifecycle(event.kind)
				reply = struct {
					OK bool `json:"ok"`
				}{err == nil}
			default:
				reply = Decision{Kind: "fallback"}
			}
			data, err := json.Marshal(reply)
			if err != nil {
				data = []byte(`{"kind":"fallback"}`)
			}
			bridge.Call("finish", event.token, string(data))
			workMu.Lock()
			inFlight--
			workMu.Unlock()
		}
	}()
	bridge.Call("register", dispatch, resume)
	return true
}
