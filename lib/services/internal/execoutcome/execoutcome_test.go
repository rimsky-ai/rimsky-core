// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package execoutcome

import "testing"

func TestErrored_SetsClassAndMessage(t *testing.T) {
	out := Errored("verifier/attribute_invalid", "attributes.url required")
	errOutcome := out.GetError()
	if errOutcome == nil {
		t.Fatal("expected an Outcome_Error")
	}
	if errOutcome.GetErrorClass() != "verifier/attribute_invalid" {
		t.Fatalf("error_class = %q", errOutcome.GetErrorClass())
	}
	if got := errOutcome.GetPayload().AsMap()["message"]; got != "attributes.url required" {
		t.Fatalf("payload.message = %v", got)
	}
}
