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

func TestStubSuccess_SetsChangeSummary(t *testing.T) {
	out := StubSuccess("verifier-http stub")
	success := out.GetSuccess()
	if success == nil {
		t.Fatal("expected an Outcome_Success")
	}
	if success.GetChangeSummary() != "verifier-http stub" {
		t.Fatalf("change_summary = %q", success.GetChangeSummary())
	}
	if success.GetChanged() {
		t.Fatal("stub success outcome must not report Changed")
	}
	if got := success.GetAttributesDelta().AsMap()["stub"]; got != true {
		t.Fatalf("attributes_delta.stub = %v", got)
	}
}
