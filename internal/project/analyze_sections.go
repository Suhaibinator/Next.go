package project

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

type WASMSection struct {
	ID           byte   `json:"id"`
	Name         string `json:"name"`
	PayloadBytes int64  `json:"payloadBytes"`
	FramingBytes int64  `json:"framingBytes"`
}

// Summarize section envelopes, not opcodes or compiler symbols. Stripped Go
// executables do not promise a name section or per-package byte attribution.
func wasmSections(data []byte) ([]WASMSection, error) {
	if len(data) < 8 || !bytes.Equal(data[:8], []byte{'\x00', 'a', 's', 'm', 1, 0, 0, 0}) {
		return nil, fmt.Errorf("expected WebAssembly version 1 header")
	}
	names := [...]string{"custom", "type", "import", "function", "table", "memory", "global", "export", "start", "element", "code", "data", "data-count", "tag"}
	seen := make(map[byte]bool)
	result := []WASMSection{}
	for offset := 8; offset < len(data); {
		if len(result) == 256 {
			return nil, fmt.Errorf("WASM section count exceeds 256")
		}
		start := offset
		id := data[offset]
		offset++
		if int(id) >= len(names) || id != 0 && seen[id] {
			return nil, fmt.Errorf("invalid or duplicate WASM section")
		}
		seen[id] = true
		size, used, err := wasmU32(data[offset:])
		if err != nil {
			return nil, err
		}
		offset += used
		if uint64(size) > uint64(len(data)-offset) {
			return nil, fmt.Errorf("truncated WASM section")
		}
		name := names[id]
		if id == 0 {
			payload := data[offset : offset+int(size)]
			length, used, err := wasmU32(payload)
			if err != nil || length > 128 || uint64(length) > uint64(len(payload)-used) {
				return nil, fmt.Errorf("invalid or oversized WASM custom section name")
			}
			text := payload[used : used+int(length)]
			if !utf8.Valid(text) {
				return nil, fmt.Errorf("invalid WASM custom section name encoding")
			}
			name = "custom:" + string(text)
		}
		result = append(result, WASMSection{id, name, int64(size), int64(offset - start)})
		offset += int(size)
	}
	return result, nil
}

func wasmU32(data []byte) (uint32, int, error) {
	var result uint32
	for i := 0; i < len(data) && i < 5; i++ {
		value := data[i]
		if i == 4 && value&0xf0 != 0 {
			break
		}
		result |= uint32(value&127) << uint(7*i)
		if value&128 == 0 {
			return result, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("invalid or truncated WASM u32 length")
}
