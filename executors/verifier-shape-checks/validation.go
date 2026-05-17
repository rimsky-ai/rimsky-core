// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// validation.go implements the Validation mix-in service-protocol for
// verifier-shape-checks. Validate is called by rimsky's control-api at
// template registration with role="executor"; the verifier inspects
// the resolved userdata + claim aliases and surfaces structural
// problems (missing checks, malformed config, unknown check kinds) as
// errors / warnings before the template is deployed.
//
// Per spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Protocol surfaces / Validation.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fallguy/rimsky/executors/verifier-shape-checks/checks"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// ValidationServer implements genv1.ValidationServer.
type ValidationServer struct {
	genv1.UnimplementedValidationServer
}

// NewValidationServer constructs the validation handler.
func NewValidationServer() *ValidationServer { return &ValidationServer{} }

// knownCheckKinds returns the snake_case names of every check the
// verifier supports. Drawn from the checks package's registry; kept
// in sync there.
var knownCheckKinds = map[string]bool{
	"no_nulls":           true,
	"pk_unique":          true,
	"row_count_absolute": true,
	"row_count_relative": true,
	"value_in_set":       true,
	"value_matches":      true,
	"min_max":            true,
	"foreign_key":        true,
}

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

	var userdata map[string]any
	if len(exec.GetUserdata()) > 0 {
		if err := json.Unmarshal(exec.GetUserdata(), &userdata); err != nil {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "invalid_userdata",
				Message: fmt.Sprintf("userdata is not valid JSON: %v", err),
				Path:    "/executor/userdata",
			})
			return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
		}
	}

	rawChecks, ok := userdata["checks"].([]any)
	if !ok {
		errors = append(errors, &genv1.ValidationFinding{
			Class:   "missing_checks",
			Message: "userdata.checks (non-empty array) required",
			Path:    "/executor/userdata/checks",
		})
		return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
	}
	if len(rawChecks) == 0 {
		errors = append(errors, &genv1.ValidationFinding{
			Class:   "empty_checks",
			Message: "userdata.checks must be non-empty",
			Path:    "/executor/userdata/checks",
		})
		return &genv1.ValidateResponse{Valid: false, Errors: errors, Warnings: warnings}
	}

	for i, raw := range rawChecks {
		obj, ok := raw.(map[string]any)
		if !ok {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "malformed_check",
				Message: fmt.Sprintf("userdata.checks[%d] must be an object", i),
				Path:    fmt.Sprintf("/executor/userdata/checks/%d", i),
			})
			continue
		}
		kind, _ := obj["kind"].(string)
		if kind == "" {
			errors = append(errors, &genv1.ValidationFinding{
				Class:   "missing_check_kind",
				Message: fmt.Sprintf("userdata.checks[%d].kind required", i),
				Path:    fmt.Sprintf("/executor/userdata/checks/%d/kind", i),
			})
			continue
		}
		if !knownCheckKinds[kind] {
			warnings = append(warnings, &genv1.ValidationFinding{
				Class:   "unknown_check_kind",
				Message: fmt.Sprintf("userdata.checks[%d].kind=%q is not a registered shape check; will fail at runtime", i, kind),
				Path:    fmt.Sprintf("/executor/userdata/checks/%d/kind", i),
			})
		}
	}
	return &genv1.ValidateResponse{
		Valid:    len(errors) == 0,
		Errors:   errors,
		Warnings: warnings,
	}
}

// keepCompilerHappy retains references that linters might otherwise
// classify as unused (checks package is needed for the kind list).
var _ = checks.CheckSpec{}
