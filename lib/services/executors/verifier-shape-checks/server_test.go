// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/errorclasses"
)

func buildReq(t *testing.T, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier", Attributes: st}
}

func TestExecute_PassAllChecks(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "no_nulls", "config": map[string]any{"field": "id"}},
			map[string]any{"kind": "pk_unique", "config": map[string]any{"field": "id"}},
		},
		"rows": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b"},
		},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	delta := success.GetAttributesDelta().AsMap()
	if delta["verifier_pass"] != true {
		t.Errorf("verifier_pass=%v, want true", delta["verifier_pass"])
	}
	if delta["verifier_checks"].(float64) != 2 {
		t.Errorf("verifier_checks=%v, want 2", delta["verifier_checks"])
	}
}

func TestExecute_FailureClassifiesAsHierarchicalCheckFailed(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "pk_unique", "config": map[string]any{"field": "id"}},
		},
		"rows": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "a"},
		},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	// @concept: signal
	if errOut.GetErrorClass() != "verifier/check_failed/pk_unique" {
		t.Errorf("error_class: got %q, want verifier/check_failed/pk_unique", errOut.GetErrorClass())
	}
	payload := errOut.GetPayload().AsMap()
	if _, ok := payload["failures"]; !ok {
		t.Errorf("expected `failures` in error payload, got %+v", payload)
	}
}

func TestExecute_InvalidAttributes_MissingChecks(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"rows": []any{map[string]any{"id": "x"}},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecute_InvalidAttributes_BadCheckKind(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"config": map[string]any{"field": "id"}},
		},
		"rows": []any{map[string]any{"id": "x"}},
	})
	outcome, _ := srv.Execute(context.Background(), req)
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecute_InvalidAttributes_BadSeverity(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{
				"kind":     "pk_unique",
				"severity": "warn",
				"config":   map[string]any{"field": "id"},
			},
		},
		"rows": []any{map[string]any{"id": "x"}},
	})
	outcome, _ := srv.Execute(context.Background(), req)
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecute_RuntimeUnknownKindClassifiesAsAttributeInvalidNotCheckFailed(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "totally_unknown_check_kind", "config": map[string]any{"field": "id"}},
		},
		"rows": []any{map[string]any{"id": "x"}},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("a misconfigured check (unknown kind) must classify as attribute_invalid, not check_failed; got %q", errOut.GetErrorClass())
	}
}

func TestExecute_MisconfiguredWarningSeverityCheckStillErrorsNotSilentlyDowngraded(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "regex_match", "severity": "warning", "config": map[string]any{"field": "id", "pattern": "("}},
		},
		"rows": []any{map[string]any{"id": "x"}},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("a misconfigured check under severity=warning must still error, not silently pass as a non-blocking warning; got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: got %q, want verifier/attribute_invalid", errOut.GetErrorClass())
	}
}

func TestExecute_RowsPresentButWrongTypeIsAttributeInvalid(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "no_nulls", "config": map[string]any{"field": "id"}},
		},
		"rows": "not-an-array",
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("a present-but-non-array rows attribute must error, not vacuously pass as zero rows; got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: got %q, want verifier/attribute_invalid", errOut.GetErrorClass())
	}
}

func TestExecute_RowsAbsentWithRowLevelCheckIsAttributeInvalid(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "no_nulls", "config": map[string]any{"field": "id"}},
		},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("an absent rows attribute for a row-level check must error, not vacuously pass zero rows; got %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: got %q, want verifier/attribute_invalid", errOut.GetErrorClass())
	}
}

func TestExecute_RowsAbsentWithOnlyCountLevelChecksSucceeds(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"checks": []any{
			map[string]any{"kind": "row_count_absolute", "config": map[string]any{"min": float64(0)}},
		},
	})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.GetSuccess() == nil {
		t.Fatalf("row_count_absolute needs no rows attribute (it checks len(rows)); expected Success, got %T", outcome.GetOutcome())
	}
}

func TestCapabilities_AdvertisesHierarchicalErrorClasses(t *testing.T) {
	obs := NewObservabilityServer()
	caps, err := obs.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	declared := caps.GetDeclaredErrorClasses()
	want := errorclasses.Declared()
	if len(declared) != len(want) {
		t.Fatalf("declared_error_classes: got %v, want %v", declared, want)
	}
	for i, c := range declared {
		if c != want[i] {
			t.Errorf("declared[%d]: got %q, want %q", i, c, want[i])
		}
		if c == "" {
			t.Errorf("declared[%d]: empty string", i)
		}
	}
}

func TestCapabilities_AdvertisesValidationSupportedRoles(t *testing.T) {
	obs := NewObservabilityServer()
	caps, err := obs.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	roles := caps.GetValidationSupportedRoles()
	if len(roles) != 1 || roles[0] != "executor" {
		t.Fatalf("validation_supported_roles: got %v, want [executor]", roles)
	}
}

func TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks(t *testing.T) {
	srv := NewServer(false)

	t.Run("warning_fail_is_non_blocking", func(t *testing.T) {
		req := buildReq(t, map[string]any{
			"checks": []any{
				map[string]any{
					"kind":     "pk_unique",
					"severity": "warning",
					"config":   map[string]any{"field": "id"},
				},
				map[string]any{
					"kind":     "no_nulls",
					"severity": "error",
					"config":   map[string]any{"field": "id"},
				},
			},
			"rows": []any{
				map[string]any{"id": "a"},
				map[string]any{"id": "a"},
			},
		})
		outcome, err := srv.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		success := outcome.GetSuccess()
		if success == nil {
			t.Fatalf("expected Success (warning-only failure must not block), got %T", outcome.GetOutcome())
		}
		delta := success.GetAttributesDelta()
		if delta == nil {
			t.Fatal("Success carried no attributes_delta")
		}
		if !deltaSurfacesWarning(delta, "pk_unique") {
			t.Errorf("expected warning finding for pk_unique surfaced in delta, got %v", delta.AsMap())
		}
	})

	t.Run("error_fail_blocks", func(t *testing.T) {
		req := buildReq(t, map[string]any{
			"checks": []any{
				map[string]any{
					"kind":     "pk_unique",
					"severity": "error",
					"config":   map[string]any{"field": "id"},
				},
			},
			"rows": []any{
				map[string]any{"id": "a"},
				map[string]any{"id": "a"},
			},
		})
		outcome, err := srv.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		errOut := outcome.GetError()
		if errOut == nil {
			t.Fatalf("expected Error (error-severity failure must block), got %T", outcome.GetOutcome())
		}
		if errOut.GetErrorClass() != "verifier/check_failed/pk_unique" {
			t.Errorf("error_class: got %q, want verifier/check_failed/pk_unique", errOut.GetErrorClass())
		}
	})
}

func deltaSurfacesWarning(delta *structpb.Struct, kind string) bool {
	return structValueMentions(structpb.NewStructValue(delta), "warning", kind)
}

func structValueMentions(v *structpb.Value, needleKey, kind string) bool {
	switch kv := v.GetKind().(type) {
	case *structpb.Value_StructValue:
		for key, field := range kv.StructValue.GetFields() {
			if containsFold(key, needleKey) && valueMentions(field, kind) {
				return true
			}
			if structValueMentions(field, needleKey, kind) {
				return true
			}
		}
	case *structpb.Value_ListValue:
		for _, item := range kv.ListValue.GetValues() {
			if structValueMentions(item, needleKey, kind) {
				return true
			}
		}
	}
	return false
}

func valueMentions(v *structpb.Value, kind string) bool {
	switch kv := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		return containsFold(kv.StringValue, kind)
	case *structpb.Value_StructValue:
		for _, field := range kv.StructValue.GetFields() {
			if valueMentions(field, kind) {
				return true
			}
		}
	case *structpb.Value_ListValue:
		for _, item := range kv.ListValue.GetValues() {
			if valueMentions(item, kind) {
				return true
			}
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func TestExecute_StubProbeShortCircuits(t *testing.T) {
	srv := NewServer(true)
	req := buildReq(t, map[string]any{"stub_probe": true})
	outcome, err := srv.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.GetSuccess() == nil {
		t.Errorf("expected Success terminal in stub mode: %T", outcome.GetOutcome())
	}
}
