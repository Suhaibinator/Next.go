package refreshcounter

type Props struct {
	Instance string `json:"instance"`
	Label    string `json:"label"`
	Initial  int    `json:"initial"`
	Enabled  bool   `json:"enabled"`
	WorkURL  string `json:"workURL"`
}
