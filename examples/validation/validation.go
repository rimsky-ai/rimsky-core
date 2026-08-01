// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Validation struct {
	genv1.UnimplementedValidationServer
}

func newValidation() *Validation { return &Validation{} }

func (v *Validation) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	if exec := req.GetExecutor(); exec != nil {
		return validateExecutor(exec), nil
	}
	if cp := req.GetClaimProducer(); cp != nil {
		return validateClaimProducer(cp), nil
	}
	return &genv1.ValidateResponse{Valid: true}, nil
}

func validateExecutor(exec *genv1.ExecutorContext) *genv1.ValidateResponse {
	if schema := exec.GetAttributesSchema(); len(schema) > 0 && !json.Valid(schema) {
		return &genv1.ValidateResponse{
			Valid: false,
			Errors: []*genv1.ValidationFinding{{
				Class:   "attributes_schema_not_json",
				Message: "executor attributes_schema is not valid JSON",
				Path:    "/executor/attributes_schema",
			}},
		}
	}
	return &genv1.ValidateResponse{Valid: true}
}

const SelectorTriggerError = "trigger-validation-error"

const SelectorTriggerWarning = "trigger-validation-warning"

func validateClaimProducer(cp *genv1.ClaimProducerContext) *genv1.ValidateResponse {
	resp := &genv1.ValidateResponse{Valid: true}
	for i, b := range cp.GetClaims() {
		sel := b.GetSelector()
		if strings.Contains(sel, SelectorTriggerError) {
			resp.Valid = false
			resp.Errors = append(resp.Errors, &genv1.ValidationFinding{
				Class:   "selector_rejected_by_example_validator",
				Message: "selector carries the example validator's error-trigger sentinel",
				Path:    selectorPath(i),
			})
			continue
		}
		if strings.Contains(sel, SelectorTriggerWarning) {
			resp.Warnings = append(resp.Warnings, &genv1.ValidationFinding{
				Class:   "selector_flagged_by_example_validator",
				Message: "selector carries the example validator's warning-trigger sentinel",
				Path:    selectorPath(i),
			})
		}
	}
	return resp
}

func selectorPath(i int) string {
	return "/claim_producer/claims/" + strconv.Itoa(i) + "/selector"
}
