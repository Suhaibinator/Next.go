package workspace

// Props contains public constants only. Committed browser URLs supply all route
// values; the native seed does not serialize a path, query or private record.
type Props struct {
	Label   string `json:"label"`
	Initial int    `json:"initial"`
}
