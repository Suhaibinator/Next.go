package model

import "testing"

func TestChoiceIdentityCloneAndValidation(t *testing.T) {
	v := Initial(true)
	copy := Clone(v)
	copy.Topics[0] = 1
	if v.Topics[0] != 0 || !v.Channel.Selected || v.Channel.Value != "" {
		t.Fatal(v, copy)
	}
	values, err := SelectedValues([]int{3, 1, 0})
	if err != nil || len(values) != 3 || values[0] != "notes" || values[1] != "notes" || values[2] != "" {
		t.Fatal(values, err)
	}
	v.Topics = []int{1, 1}
	if Validate(v)["topics"] == "" {
		t.Fatal("duplicate index accepted")
	}
	v = Initial(false)
	if ChannelSummary(v.Channel) != "No selection" || len(Validate(v)) != 0 {
		t.Fatal(v, Validate(v))
	}
}
