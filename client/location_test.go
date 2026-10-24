package client

import "testing"

func TestLocationQueryReturnsOwnedGoValues(t *testing.T) {
	value := Location{Search: "?tag=one&tag=two&q=Go+WASM&empty=&bad=%zz&semi=x;y"}
	first := value.Query()
	if first.Get("q") != "Go WASM" || len(first["tag"]) != 2 || first["tag"][1] != "two" {
		t.Fatal(first)
	}
	if _, present := first["empty"]; !present {
		t.Fatal("empty value lost")
	}
	if _, present := first["bad"]; present {
		t.Fatal("malformed field unexpectedly parsed")
	}
	first["tag"][0] = "changed"
	first.Set("q", "changed")
	if next := value.Query(); next.Get("q") != "Go WASM" || next.Get("tag") != "one" {
		t.Fatal("snapshot was mutated", next)
	}
}
