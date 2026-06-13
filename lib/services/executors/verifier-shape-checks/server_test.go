// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/errorclasses"
)

// fakeStream collects every event the server emits; assertion runs
// after executeCore returns.
type fakeStream struct {
	events []*genv1.ExecuteEvent
}

func (f *fakeStream) send(ev *genv1.ExecuteEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStream) terminal() *genv1.StreamClose {
	for _, ev := range f.events {
		if sc := ev.GetStreamClose(); sc != nil {
			return sc
		}
	}
	return nil
}

func buildReq(t *testing.T, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier", Attributes: st}
}

func TestExecuteCore_PassAllChecks(t *testing.T) {
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
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	if term.GetSuccess() == nil {
		t.Errorf("expected Success, got %T", term.Outcome)
	}
}

func TestExecuteCore_FailureClassifiesAsHierarchicalCheckFailed(t *testing.T) {
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
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", term.Outcome)
	}
	// Hierarchical leaf carries the failed check's kind suffix per
	// `concept:signal`. Validator accepts this via the declared
	// `verifier/check_failed/*` wildcard.
	if errOut.GetErrorClass() != "verifier/check_failed/pk_unique" {
		t.Errorf("error_class: got %q, want verifier/check_failed/pk_unique", errOut.GetErrorClass())
	}
}

func TestExecuteCore_InvalidAttributesRejected(t *testing.T) {
	srv := NewServer(false)
	req := buildReq(t, map[string]any{
		"rows": []any{map[string]any{"id": "x"}},
		// missing `checks`
	})
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", term.Outcome)
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

// TestCapabilities_AdvertisesHierarchicalErrorClasses confirms the
// observability surface advertises the canonical verifier/* leaves
// imported from `pkg:executors/verifier-shape-checks/errorclasses`.
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

// TestCapabilities_AdvertisesValidationSupportedRoles confirms the
// observability handshake advertises the Validation mix-in's supported
// roles. The verifier's Validate handles role="executor"; rimsky's
// registry learns roles only from this field, so an empty list would
// leave the validator dialed but never selected at registration time.
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

// TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks
// drives the real executeCore dispatch over a real rows payload and
// asserts the severity partition (`S-executors-verifier-severity-partition`):
// a failed `severity:warning` check is recorded as a non-blocking finding
// and does NOT block the dispatch, while a failed `severity:error` check
// drives the `verifier/check_failed/<kind>` Error terminal.
//
// RED today: severity is not yet consumed, so EVERY failed check blocks —
// Dispatch A's failing warning check produces an Error terminal instead of
// the expected Success. A later GREEN pass partitions failures by severity.
func TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks(t *testing.T) {
	srv := NewServer(false)

	// Dispatch A: a failing warning-severity check (pk_unique over a column
	// with a duplicate) alongside a passing error-severity check (no_nulls).
	// Because the only failure is warning-severity, the dispatch must succeed
	// and surface the warning as a non-blocking finding in the delta/summary.
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
				map[string]any{"id": "a"}, // duplicate → pk_unique (warning) FAILS
			},
		})
		fs := &fakeStream{}
		if err := srv.executeCore(req, fs.send); err != nil {
			t.Fatal(err)
		}
		term := fs.terminal()
		if term == nil {
			t.Fatal("no terminal event")
		}
		if term.GetSuccess() == nil {
			t.Fatalf("expected Success terminal (warning-only failure must not block), got %T", term.Outcome)
		}
		// The non-blocking warning failure must be observable: assert the
		// failed warning check's kind is surfaced in the Success delta's
		// warnings list (the operator needs to see the soft finding even
		// though it did not block).
		delta := term.GetSuccess().GetAttributesDelta()
		if delta == nil {
			t.Fatal("Success terminal carried no attributes_delta")
		}
		if !deltaSurfacesWarning(delta, "pk_unique") {
			t.Errorf("expected warning finding for pk_unique surfaced in success delta, got %v", delta.AsMap())
		}
	})

	// Dispatch B: a failing error-severity check must block with the
	// hierarchical `verifier/check_failed/<kind>` Error terminal.
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
				map[string]any{"id": "a"}, // duplicate → pk_unique (error) FAILS
			},
		})
		fs := &fakeStream{}
		if err := srv.executeCore(req, fs.send); err != nil {
			t.Fatal(err)
		}
		term := fs.terminal()
		if term == nil {
			t.Fatal("no terminal event")
		}
		errOut := term.GetError()
		if errOut == nil {
			t.Fatalf("expected Error terminal (error-severity failure must block), got %T", term.Outcome)
		}
		if errOut.GetErrorClass() != "verifier/check_failed/pk_unique" {
			t.Errorf("error_class: got %q, want verifier/check_failed/pk_unique", errOut.GetErrorClass())
		}
	})
}

// deltaSurfacesWarning reports whether the Success attributes_delta carries
// a non-blocking-warning finding referencing the named check kind. The
// GREEN pass populates a `warnings` collection in the delta; this helper
// scans the delta's structpb values for the kind so the assertion does not
// over-constrain the exact field shape the implementer chooses.
func deltaSurfacesWarning(delta *structpb.Struct, kind string) bool {
	return structValueMentions(structpb.NewStructValue(delta), "warning", kind)
}

// structValueMentions walks a structpb.Value tree and reports whether any
// field whose key contains needleKey carries (anywhere beneath it) a value
// mentioning the kind string.
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

// valueMentions reports whether v (a leaf string, or a struct/list
// containing one) mentions kind.
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

func TestExecuteCore_StubProbeShortCircuits(t *testing.T) {
	srv := NewServer(true)
	req := buildReq(t, map[string]any{"stub_probe": true})
	fs := &fakeStream{}
	if err := srv.executeCore(req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil || term.GetSuccess() == nil {
		t.Errorf("expected Success terminal in stub mode: %+v", term)
	}
}
