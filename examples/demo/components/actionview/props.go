package actionview

type Props struct {
	Count, Revision int64
	Action          string
}

// ReceiptEvent carries public demonstration data between the optional lazy
// Go runtime and the eager page. It is an observation, never an authority.
const ReceiptEvent = "nextgo-demo-action-view-receipt"

type Receipt struct {
	Confirmed bool   `json:"confirmed"`
	Count     int64  `json:"count"`
	Revision  int64  `json:"revision"`
	Applied   bool   `json:"applied"`
	Status    int    `json:"status"`
	ViewError bool   `json:"viewError"`
	Mode      string `json:"mode"`
}
