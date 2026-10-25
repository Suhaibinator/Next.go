//go:build js && wasm

package client

// Admission is recorded before the portable store launches its actual loader.
// Logical waiter cancellation never retires this worker's development lease.
func beginResourceRefreshActivity() (func(), error) {
	lease, err := beginRefreshActivity()
	if err != nil || lease == nil {
		return nil, err
	}
	return lease.Done, nil
}
