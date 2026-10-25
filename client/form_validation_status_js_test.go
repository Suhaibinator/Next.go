//go:build js && wasm

package client

import "testing"

func TestFormValidationStatusPreservesSubmissionOutcome(t *testing.T) {
	state := FormSnapshot[string]{Validating: map[string]bool{"name": true}, Unconfirmed: true}
	if got := formStatus(state); got != "Checking fields…" {
		t.Fatal(got)
	}
	state.Pending = true
	if got := formStatus(state); got != "Saving…" {
		t.Fatal(got)
	}
	state.Pending = false
	state.Validating = nil
	if got := formStatus(state); got != "Previous submission outcome is unknown. Check the server before retrying." {
		t.Fatal(got)
	}
	state.Unconfirmed = false
	state.Phase, state.Message = FormSucceeded, "Confirmed."
	if got := formStatus(state); got != "Confirmed." {
		t.Fatal(got)
	}
}
