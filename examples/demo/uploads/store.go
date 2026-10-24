//go:build !js

package uploads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
)

var receipts = struct {
	sync.Mutex
	items map[string]Receipt
	order []string
}{items: map[string]Receipt{}}

// Receive retains metadata only. It is public, bounded, process-local demo data,
// not authenticated storage, an object store or multi-instance persistence.
func Receive(ctx context.Context, input Input) (Receipt, error) {
	receipt, err := Compute(ctx, input)
	if err != nil {
		return Receipt{}, err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return Receipt{}, err
	}
	receipt.ID = hex.EncodeToString(token)
	receipts.Lock()
	defer receipts.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if len(receipts.order) >= 256 {
		delete(receipts.items, receipts.order[0])
		receipts.order = receipts.order[1:]
	}
	receipts.order = append(receipts.order, receipt.ID)
	receipts.items[receipt.ID] = CloneReceipt(receipt)
	return receipt, nil
}
func Get(ctx context.Context, id string) (Receipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	receipts.Lock()
	defer receipts.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	receipt, ok := receipts.items[id]
	return CloneReceipt(receipt), ok, nil
}
