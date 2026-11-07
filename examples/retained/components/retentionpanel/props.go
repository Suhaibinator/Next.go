package retentionpanel

type Props struct {
	Name           string `json:"name"`
	Initial        int    `json:"initial"`
	Fail           bool   `json:"fail,omitempty"`
	RetireNavigate bool   `json:"retireNavigate,omitempty"`
	ResumeFail     bool   `json:"resumeFail,omitempty"`
	CleanupPanic   bool   `json:"cleanupPanic,omitempty"`
}
