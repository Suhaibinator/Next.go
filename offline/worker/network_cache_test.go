//go:build !js

package worker

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestBridgeDocumentNetworkBypassesHTTPDiskCache(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the isolated bridge promise test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, "testdata/network-cache.mjs", "bridge.go").CombinedOutput()
	if err != nil {
		t.Fatalf("document network request ownership: %v\n%s", err, output)
	}
}
