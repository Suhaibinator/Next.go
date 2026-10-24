//go:build !js

package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestReceiveRetainsOnlyOwnedBoundedMetadataAndChecksCancellation(t *testing.T) {
	data := []byte{0, 1, 255}
	input := Input{Title: " Binary files ", Files: []Content{{Name: "a.bin", Data: data}, {Name: "empty.txt"}}}
	receipt, err := Receive(context.Background(), input)
	digest := sha256.Sum256(data)
	if err != nil || receipt.Title != "Binary files" || len(receipt.ID) != 32 || receipt.Files[0].SHA256 != hex.EncodeToString(digest[:]) || receipt.Files[1].Size != 0 {
		t.Fatal(receipt, err)
	}
	data[0] = 10
	receipt.Files[0].Name = "outside"
	loaded, ok, err := Get(context.Background(), receipt.ID)
	if err != nil || !ok || loaded.Files[0].Name != "a.bin" {
		t.Fatal(loaded, ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Receive(ctx, input); err == nil {
		t.Fatal("canceled upload received")
	}
	for _, bad := range []Input{{Title: "label"}, {Title: "", Files: input.Files}, {Title: "label", Files: []Content{{Name: "../bad"}}}, {Title: "label", Files: []Content{{Name: "big", Data: make([]byte, MaxFileBytes+1)}}}, {Title: "label", Files: []Content{{Name: strings.Repeat("x", 256)}}}} {
		if len(Validate(bad)) == 0 {
			t.Fatal("invalid input accepted")
		}
	}
}
