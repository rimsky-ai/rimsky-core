// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// validation.go implements the Validation mix-in service-protocol for
// verifier-shape-checks. Validate is called by rimsky's control-api at
// template registration with role="executor"; the verifier inspects
// the merged effective attribute schema (looking for a `checks`
// property declared via `default:` or `source:` on the per-node L2
// schema) plus claim aliases, and surfaces structural problems
// (missing checks, malformed config, unknown check kinds) as errors /
// warnings before the template is deployed.
//
// Per spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Protocol surfaces / Validation.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/checks"
)

// ValidationServer implements genv1.ValidationServer.
type ValidationServer struct {
	genv1.UnimplementedValidationServer
}

// NewValidationServer constructs the validation handler.
func NewValidationServer() *ValidationServer { return &ValidationServer{} }

// knownCheckKinds holds the snake_case names of every check the
// verifier's runtime dispatcher supports, sourced from the checks
// package's registry so the registration-time `unknown_check_kind`
// advisory cannot drift from what Run actually dispatches.
var knownCheckKinds = checks.KnownKinds()

// Validate routes on the role discriminator. Only role="executor" is
// supported; any other role surfaces an error finding with class
// `unsupported_role` (caller can downgrade to warning via the
// validation orchestrator's policy).
func (v *ValidationServer) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	if req.GetRole() != "executor" {
		return &genv1.ValidateResponse{
			Valid: false,
			Errors: []*genv1.ValidationFinding{{
				Class:   "unsupported_role",
				Message: fmt.Sprintf("verifier-shape-checks Validation only supports role=executor; got %q", req.GetRole()),
				Path:    "/role",
			}},
		}, nil
	}
	exec := req.GetExecutor()
	if exec == nil {
		return &genv1.ValidateResponse{
			Valid: false,
			Errors: []*genv1.ValidationFinding{{
				Class:   "missing_context",
				Message: "ValidateRequest.context.executor must be set for role=executor",
				Path:    "/executor",
			}},
		}, nil
	}
	return validateExecutor(exec), nil
}

// validateExecutor parses the executor context and returns the
// findings. Exported via a small surface so the in-process self-test
// can call it directly.
func validateExecutor(exec *genv1.ExecutorContext) *genv1.ValidateResponse {
	errors := make([]*genv1.ValidationFinding, 0)
	warnings := make([]*genv1.ValidationFinding, 0)

	var attrs map[string]any
	if len(exec.GetAttributesSchema()) > 0 {
		if err := json.Unmarshal(exec.GetAttributesSchema(), &attrs); err != nil {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "invalid_attribute",
				Message: fmt.Sprintf("attributes_schema is not valid JSON: %v", err),
				Path:    "/executor/attributes_schema",
			})
			return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
		}
	}

	// Extract the `checks` attribute's static-default value from the
	// per-node L2 schema. Under the userdata collapse, this verifier
	// expects the template author to declare `checks` either as a
	// `default:` value (the common case) or via `source:` (derived at
	// dispatch from another node's attributes). Both shapes route through
	// schema.properties.checks; the registration-time gate accepts either
	// shape as satisfying the requirement, deferring per-element shape
	// validation to dispatch time when `source:` is used. Only when
	// neither is present (and the property is not `readOnly`) does the
	// gate emit `missing_checks`.
	props, _ := attrs["properties"].(map[string]any)
	checksProp, _ := props["checks"].(map[string]any)
	_, hasSource := checksProp["source"].(string)
	rawChecks, hasDefault := checksProp["default"].([]any)
	readOnly, _ := checksProp["readOnly"].(bool)
	if !hasSource && !hasDefault && !readOnly {
		errors = append(errors, &genv1.ValidationFinding{
			Class:   "missing_checks",
			Message: "attributes.checks (non-empty array) required via default: or source:",
			Path:    "/executor/attributes.checks",
		})
		return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
	}
	// Source-bound `checks` is validated at dispatch time once the
	// upstream produces the array; registration-time per-element checks
	// only run when a static default is present.
	if !hasDefault {
		return &genv1.ValidateResponse{Valid: true, Errors: errors, Warnings: warnings}
	}
	if len(rawChecks) == 0 {
		errors = append(errors, &genv1.ValidationFinding{
			Class:   "empty_checks",
			Message: "attributes.checks must be non-empty",
			Path:    "/executor/attributes.checks",
		})
		return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
	}

	for i, raw := range rawChecks {
		obj, ok := raw.(map[string]any)
		if !ok {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "malformed_check",
				Message: fmt.Sprintf("attributes.checks[%d] must be an object", i),
				Path:    fmt.Sprintf("/executor/attributes.checks/%d", i),
			})
			continue
		}
		kind, _ := obj["kind"].(string)
		if kind == "" {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "missing_check_kind",
				Message: fmt.Sprintf("attributes.checks[%d].kind required", i),
				Path:    fmt.Sprintf("/executor/attributes.checks/%d/kind", i),
			})
			continue
		}
		if !knownCheckKinds[kind] {
			warnings = append(warnings, &genv1.ValidationFinding{
				Class:   "unknown_check_kind",
				Message: fmt.Sprintf("attributes.checks[%d].kind=%q is not a registered shape check; will fail at runtime", i, kind),
				Path:    fmt.Sprintf("/executor/attributes.checks/%d/kind", i),
			})
		}
	}
	return &genv1.ValidateResponse{
		Valid:    len(errors) == 0,
		Errors:   errors,
		Warnings: warnings,
	}
}
