package clientpanel

// Props are public browser data. Failure is an explicit demonstration mode,
// supplied only by the sample page's validated query values.
type Props struct {
	Label   string `json:"label"`
	Initial int    `json:"initial"`
	Failure string `json:"failure,omitempty"`
}
