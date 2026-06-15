// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof for STORY-validation-mixin-uniform's executor leg: an
// operator configures the bundled `verifier-shape-checks` executor with
// `protocols: [executor, validation]` and the assembled stack actually
// USES the validation mix-in at template registration — the executor's
// observability Capabilities handshake advertises
// `validation_supported_roles: [executor]`, the registry learns the
// roles from that handshake (not from config), the registration
// pipeline selects the verifier for role="executor" nodes, and the
// verifier's findings gate the registration response.
//
// Falsifier brief from the story: "an executor or publisher advertising
// the mix-in whose supported-roles list is still treated as empty —
// dialed but never used." This scenario kills the executor half against
// the real bundled image (the publisher + claim-producer halves are
// covered by the registry-level conformance proof in
// lib/control/config): if the handshake-learned roles were empty, the
// pipeline would never call the verifier's Validate, the structurally
// broken template below would register with 201, and the first leg
// fails.
//
// Two legs against one stack:
//
//  1. A verifier-backed node whose attribute schema declares NO
//     `checks` (no default:, no source:, not readOnly). The verifier's
//     registration-time Validate must reject it — POST /templates
//     returns 400 with a `missing_checks` finding attributed to
//     service `verifier-shape-checks`, role `executor`.
//
//  2. The same wiring with a well-formed `checks` default (kinds drawn
//     from the runtime dispatcher's registry). Registration must
//     succeed with 201 and carry no validation_errors — proving the
//     validator gates rather than blanket-rejects.
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestVerifierRegistrationValidation drives the bundled verifier's
// Validation mix-in through the real registration surface.
func TestVerifierRegistrationValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: verifier must be up before rimsky — startup eagerly
	// runs the executor Capabilities handshake AND, because the peer
	// advertises the validation mix-in, the ExecutorObservability
	// handshake that resolves validation_supported_roles. A failed roles
	// handshake fails startup by design, so a stack that comes up at all
	// has already learned the live roles.
	netName := harness.NewNetwork(ctx, t)
	harness.StartVerifierShapeChecksOnNetwork(ctx, t, netName, "verifier-shape-checks")
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("verifier-shape-checks", "verifier-shape-checks:9095"),
		harness.WithExecutorProtocols("verifier-shape-checks", "validation"),
	)

	// @constraint: leg 1 — missing `checks` must drive the verifier's
	// Validate to reject the registration. The 400 is only reachable
	// when the handshake-learned role set contains "executor"; an empty
	// set silently skips the executor-role check and the template
	// registers.
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

	// @deliberate: leg 2 — well-formed checks drawn from the runtime
	// dispatcher's registry so registration succeeds AND the verifier's
	// unknown_check_kind advisory stays silent, proving the validator
	// gates rather than blanket-rejects.
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

// buildRegistrationValidationTemplate constructs a single-node
// verifier-backed template body. When checksDefault is nil the schema
// declares NO `checks` property at all — the shape the verifier's
// registration gate rejects with `missing_checks`; otherwise `checks`
// carries the given static default.
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
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
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
