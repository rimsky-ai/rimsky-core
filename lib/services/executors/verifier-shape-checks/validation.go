// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifiershapechecks

import (
	"context"
	"encoding/json"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/checks"
)

type ValidationServer struct {
	genv1.UnimplementedValidationServer
}

func NewValidationServer() *ValidationServer { return &ValidationServer{} }

var knownCheckKinds = checks.KnownKinds()

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

func validateExecutor(exec *genv1.ExecutorContext) *genv1.ValidateResponse {
	errors := make([]*genv1.ValidationFinding, 0)
	warnings := make([]*genv1.ValidationFinding, 0)

	var declaredSchema map[string]any
	if len(exec.GetAttributesSchema()) > 0 {
		if err := json.Unmarshal(exec.GetAttributesSchema(), &declaredSchema); err != nil {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "invalid_attribute",
				Message: fmt.Sprintf("attributes_schema is not valid JSON: %v", err),
				Path:    "/executor/attributes_schema",
			})
			return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
		}
	}

	props, _ := declaredSchema["properties"].(map[string]any)
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
