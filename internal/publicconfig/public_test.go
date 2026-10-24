package publicconfig

import (
	"strconv"
	"strings"
	"testing"
)

func TestBudgetsAndOwnership(t *testing.T) {
	original := map[string]string{"message": "public"}
	snapshot, err := Clone(original)
	if err != nil {
		t.Fatal(err)
	}
	original["message"] = "changed"
	if snapshot["message"] != "public" {
		t.Fatal("not frozen")
	}
	for _, n := range []int{65, 17} {
		values := map[string]string{}
		for i := 0; i < n; i++ {
			values[strconv.Itoa(i)] = strings.Repeat("x", 1024)
		}
		if _, err := Clone(values); err == nil {
			t.Fatal("budget ignored")
		}
	}
	for _, values := range []map[string]string{{strings.Repeat("x", 129): ""}, {"a": strings.Repeat("x", 1025)}, {"a": string([]byte{255})}, {"a": "\x00"}} {
		if _, err := Clone(values); err == nil {
			t.Fatal("invalid input")
		}
	}
}
