package client

// LinkStatus describes one actual anchor's navigation. Pending ends when the
// validated page or streamed shell commits its DOM, metadata and history. Later
// deferred content has its own loading state. A complete cached navigation can
// skip Pending entirely.
type LinkStatus struct {
	Pending bool
}
