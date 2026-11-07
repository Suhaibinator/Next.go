package panel

type Props struct {
	Name             string `json:"name"`
	Product          string `json:"product"`
	Price            string `json:"price"`
	Query            string `json:"query"`
	RawQuery         string `json:"rawQuery"`
	Version          string `json:"version"`
	Initial          int    `json:"initial"`
	NavigateOnUpdate bool   `json:"navigateOnUpdate,omitempty"`
	Reject           bool   `json:"reject,omitempty"`
	EffectFail       bool   `json:"effectFail,omitempty"`
}
