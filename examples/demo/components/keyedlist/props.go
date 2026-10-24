package keyedlist

type Item struct{ ID, Label string }
type Props struct {
	Name, Target           string
	Items                  []Item
	Mismatch               bool
	Table                  bool
	BindPanic, UpdatePanic bool
}
