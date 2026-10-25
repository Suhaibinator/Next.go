package actions

// ConflictError reports a known application concurrency conflict, using HTTP
// 409 and the public error code "conflict". Message and Fields are public data;
// never include secrets. An empty Message selects a safe default.
//
// Return this only when the application can establish that the requested write
// was rejected. This error does not authorize requests, roll back a transaction,
// make retries safe, or perform automatic reconciliation.
type ConflictError struct {
	Message string
	Fields  map[string]string
}

func (e *ConflictError) Error() string {
	if e.Message == "" {
		return "The resource changed. Refresh and reconcile before submitting again."
	}
	return e.Message
}
