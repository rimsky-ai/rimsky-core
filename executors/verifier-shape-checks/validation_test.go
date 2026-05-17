// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"testing"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// TestValidate_ExecutorHappy covers a well-shaped executor context
// with a known check kind. Expect Valid=true and zero errors.
func TestValidate_ExecutorHappy(t *testing.T) {
	v := NewValidationServer()
	userdata, _ := json.Marshal(map[string]any{
		"checks": []map[string]any{
			{"kind": "no_nulls", "config": map[string]any{"field": "id"}},
		},
	})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias: "verify",
			Userdata:  userdata,
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

// TestValidate_ExecutorUnknownKind surfaces a warning (not error)
// for an unrecognized check kind.
func TestValidate_ExecutorUnknownKind(t *testing.T) {
	v := NewValidationServer()
	userdata, _ := json.Marshal(map[string]any{
		"checks": []map[string]any{
			{"kind": "totally_not_a_real_check"},
		},
	})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias: "verify",
			Userdata:  userdata,
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

// TestValidate_ExecutorMalformedJSON surfaces an invalid_userdata
// error and short-circuits before walking checks.
func TestValidate_ExecutorMalformedJSON(t *testing.T) {
	v := NewValidationServer()
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias: "verify",
			Userdata:  []byte("not json {"),
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

// TestValidate_UnsupportedRole rejects a non-executor role with an
// unsupported_role error finding.
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

// TestValidate_MissingExecutorContext surfaces missing_context.
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

// TestValidate_EmptyChecks rejects an empty checks list.
func TestValidate_EmptyChecks(t *testing.T) {
	v := NewValidationServer()
	userdata, _ := json.Marshal(map[string]any{"checks": []any{}})
	resp, err := v.Validate(context.Background(), &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias: "verify",
			Userdata:  userdata,
		}},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.GetValid() {
		t.Error("expected Valid=false for empty checks list")
	}
}
