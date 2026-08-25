// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package stubprobe

import (
	"strings"
	"testing"
)

func TestSuccessDelta_DefaultsToTheStubMarker(t *testing.T) {
	delta, err := SuccessDelta(map[string]any{"url": "http://unreachable.invalid/"})
	if err != nil {
		t.Fatal(err)
	}
	if delta["stub"] != true {
		t.Errorf("delta = %+v, want stub:true", delta)
	}
}

func TestSuccessDelta_ReplacesTheDefaultWithTheRequestedObject(t *testing.T) {
	attrs := map[string]any{"stub_response": map[string]any{"id": "abc", "done": true}}
	delta, err := SuccessDelta(attrs)
	if err != nil {
		t.Fatal(err)
	}
	if delta["id"] != "abc" || delta["done"] != true {
		t.Fatalf("delta = %+v", delta)
	}
	if _, marked := delta["stub"]; marked {
		t.Errorf("the override replaces the default delta, got %+v", delta)
	}
	delta["added-by-the-caller"] = true
	if _, leaked := attrs["stub_response"].(map[string]any)["added-by-the-caller"]; leaked {
		t.Error("the delta aliases the request attributes; a caller's edit reaches the request")
	}
}

func TestSuccessDelta_RefusesAnOverrideThatIsNotAnObject(t *testing.T) {
	_, err := SuccessDelta(map[string]any{"stub_response": "not-an-object"})
	if err == nil {
		t.Fatal("expected an error for a non-object stub_response")
	}
	if !strings.Contains(err.Error(), "stub_response must be a JSON object") {
		t.Errorf("error does not name the requirement: %v", err)
	}
}

func TestSuccess_CarriesTheOverrideAndTheRequestedTags(t *testing.T) {
	outcome := Success(StubSuccess{
		Attributes: map[string]any{
			"stub_response": map[string]any{"id": "abc"},
			"stub_tags":     []any{"fresh", "checked"},
		},
		ChangeSummary: "stub",
		ErrorClass:    "http/attribute_invalid",
		Changed:       true,
		Scratch:       []byte("carried"),
	})
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	if success.GetAttributesDelta().AsMap()["id"] != "abc" {
		t.Errorf("attributes_delta = %+v", success.GetAttributesDelta().AsMap())
	}
	if got := success.GetTags(); len(got) != 2 || got[0] != "fresh" || got[1] != "checked" {
		t.Errorf("tags = %v", got)
	}
	if string(success.GetScratch()) != "carried" {
		t.Errorf("scratch = %q", success.GetScratch())
	}
	if success.GetChangeSummary() != "stub" {
		t.Errorf("change_summary = %q", success.GetChangeSummary())
	}
}

func TestSuccess_AnswersAnErrorInTheCallersClassForANonObjectOverride(t *testing.T) {
	outcome := Success(StubSuccess{
		Attributes: map[string]any{"stub_response": 7},
		ErrorClass: "verifier/attribute_invalid",
	})
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class = %q", errOut.GetErrorClass())
	}
}

func TestSuccess_AnswersAnErrorInTheCallersClassForANonArrayTagsValue(t *testing.T) {
	outcome := Success(StubSuccess{
		Attributes: map[string]any{"stub_tags": "fresh"},
		ErrorClass: "verifier/attribute_invalid",
	})
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class = %q", errOut.GetErrorClass())
	}
}

func TestSuccess_AnswersAnErrorNamingTheTagThatIsNotAString(t *testing.T) {
	outcome := Success(StubSuccess{
		Attributes: map[string]any{"stub_tags": []any{"fresh", 7}},
		ErrorClass: "verifier/attribute_invalid",
	})
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	payload := errOut.GetPayload().AsMap()
	found := false
	for _, v := range payload {
		if text, isString := v.(string); isString && strings.Contains(text, "stub_tags[1]") {
			found = true
		}
	}
	if !found {
		t.Errorf("the error must name the entry that is not a string; payload = %+v", payload)
	}
}

func TestTags_AbsentAttributeIsNoTagsAndNoError(t *testing.T) {
	tags, err := Tags(map[string]any{})
	if err != nil || tags != nil {
		t.Errorf("Tags() = %v, %v; want no tags and no error", tags, err)
	}
}
