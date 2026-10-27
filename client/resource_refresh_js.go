//go:build js && wasm

package client

// Admission is recorded before the portable store launches its actual loader.
// Logical waiter cancellation retires neither the refresh nor retained lease.
// A retained resource waiter and its physical loader are separate charges.
func beginResourceRefreshActivity() (func(), error) {
	lease, err := beginRefreshActivity()
	if err != nil {
		return nil, err
	}
	physical, err := beginRetainedTask()
	if err != nil {
		lease.Done()
		return nil, err
	}
	if lease == nil && physical == nil {
		return nil, nil
	}
	return func() { lease.Done(); physical.Done() }, nil
}
