// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestVerifierRegistrationValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	verifierEP := harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", verifierEP),
		harness.WithExecutorProtocols("verifier-shape-checks", "validation"),
	)

	t.Run("missing_checks_rejected_at_registration", func(t *testing.T) {
		status, raw := ep.PostJSON(t, "/v1/templates",
			buildRegistrationValidationTemplate("registration-validation-missing-checks", nil))
		if status != http.StatusBadRequest {
			t.Fatalf("POST /templates without checks: got %d (want 400 — verifier validation never ran?): %s",
				status, string(raw))
		}
		var resp struct {
			ValidationErrors []struct {
				ServiceName string `json:"service_name"`
				Role        string `json:"role"`
				Class       string `json:"class"`
			} `json:"validation_errors"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode 400 body: %v: %s", err, string(raw))
		}
		found := false
		for _, e := range resp.ValidationErrors {
			if e.ServiceName == "verifier-shape-checks" && e.Role == "executor" && e.Class == "missing_checks" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a missing_checks finding from verifier-shape-checks (role=executor), got: %s",
				string(raw))
		}
	})

	t.Run("well_formed_checks_registered", func(t *testing.T) {
		status, raw := ep.PostJSON(t, "/v1/templates",
			buildRegistrationValidationTemplate("registration-validation-well-formed", []any{
				map[string]any{
					"kind":     "no_nulls",
					"severity": "error",
					"config":   map[string]any{"field": "id"},
				},
				map[string]any{
					"kind":     "numeric_range",
					"severity": "warning",
					"config":   map[string]any{"field": "value", "min": float64(0), "max": float64(100)},
				},
			}))
		if status != http.StatusCreated {
			t.Fatalf("POST /templates with well-formed checks: got %d (want 201): %s", status, string(raw))
		}
		var resp struct {
			TemplateID         string `json:"template_id"`
			ValidationWarnings []any  `json:"validation_warnings"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode 201 body: %v: %s", err, string(raw))
		}
		if resp.TemplateID == "" {
			t.Fatalf("template_id empty: %s", string(raw))
		}
		if len(resp.ValidationWarnings) != 0 {
			t.Fatalf("expected no validation warnings for registry-known check kinds, got: %s", string(raw))
		}
	})
}

func buildRegistrationValidationTemplate(name string, checksDefault []any) map[string]any {
	properties := map[string]any{
		"rows": map[string]any{
			"type":        "array",
			"description": "tabular payload to verify",
			"default": []any{
				map[string]any{"id": "alpha", "value": float64(10)},
			},
		},
	}
	if checksDefault != nil {
		properties["checks"] = map[string]any{
			"type":        "array",
			"description": "shape checks to run against rows",
			"default":     checksDefault,
		}
	}
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "verifier",
					"executor": "verifier-shape-checks",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type":       "object",
							"properties": properties,
						},
					},
				},
			},
		},
	}
}
