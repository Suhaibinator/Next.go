package linkhint

type Link struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}
type Props struct {
	Label string `json:"label"`
	Links []Link `json:"links"`
}
