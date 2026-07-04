// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifiershapechecks

import (
	"context"
	"encoding/json"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func schemaWithChecks(checks any) []byte {
	out, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"checks": map[string]any{
				"type":    "array",
				"default": checks,
			},
		},
	})
	return out
}

func TestValidate_ExecutorHappy(t *testing.T) {
	v := NewValidationServer()
	schema := schemaWithChecks([]map[string]any{
		{"kind": "no_nulls", "config": map[string]any{"field": "id"}},
	})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "verify",
			AttributesSchema: schema,
		}},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !resp.GetValid() {
		t.Errorf("expected Valid=true, got false; errors=%+v", resp.GetErrors())
	}
	if len(resp.GetErrors()) != 0 {
		t.Errorf("expected 0 errors, got %d", len(resp.GetErrors()))
	}
}

func TestValidate_ExecutorUnknownKind(t *testing.T) {
	v := NewValidationServer()
	schema := schemaWithChecks([]map[string]any{
		{"kind": "totally_not_a_real_check"},
	})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "verify",
			AttributesSchema: schema,
		}},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !resp.GetValid() {
		t.Errorf("expected Valid=true (warnings are not blocking), got false; errors=%+v", resp.GetErrors())
	}
	if len(resp.GetWarnings()) == 0 {
		t.Error("expected at least one warning for unknown_check_kind")
	}
}

func TestValidate_ExecutorMalformedJSON(t *testing.T) {
	v := NewValidationServer()
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "verify",
			AttributesSchema: []byte("not json {"),
		}},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.GetValid() {
		t.Error("expected Valid=false for malformed JSON")
	}
	if len(resp.GetErrors()) == 0 {
		t.Error("expected error findings for malformed JSON")
	}
}

func TestValidate_UnsupportedRole(t *testing.T) {
	v := NewValidationServer()
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{Role: "claim_producer"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.GetValid() {
		t.Error("expected Valid=false for unsupported role")
	}
	if len(resp.GetErrors()) == 0 || resp.GetErrors()[0].GetClass() != "unsupported_role" {
		t.Errorf("expected unsupported_role error, got %+v", resp.GetErrors())
	}
}

func TestValidate_MissingExecutorContext(t *testing.T) {
	v := NewValidationServer()
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{Role: "executor"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.GetValid() {
		t.Error("expected Valid=false with missing executor context")
	}
}

func TestValidate_EmptyChecks(t *testing.T) {
	v := NewValidationServer()
	schema := schemaWithChecks([]any{})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "verify",
			AttributesSchema: schema,
		}},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.GetValid() {
		t.Error("expected Valid=false for empty checks list")
	}
}

func TestValidate_SourceBoundChecks(t *testing.T) {
	v := NewValidationServer()
	schemaBytes, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"checks": map[string]any{
				"type":   "array",
				"source": "{{nodes.upstream.attribute.shape_checks}}",
			},
		},
	})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "verify",
			AttributesSchema: schemaBytes,
		}},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !resp.GetValid() {
		t.Errorf("expected Valid=true for source-bound checks; errors=%+v", resp.GetErrors())
	}
}
