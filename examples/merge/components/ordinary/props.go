package ordinary

type Props struct {
	Caption       string `json:"caption"`
	Boot          bool   `json:"boot,omitempty"`
	InitialSource bool   `json:"initialSource,omitempty"`
}
