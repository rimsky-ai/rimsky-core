// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package main is a minimal, copy-and-modify Validation service: a mix-in
// protocol any service binary may advertise alongside its primary protocol
// (executor, claim_producer, lifecycle_subscriber, sensor). rimsky invokes
// Validate at template registration with the role-specific context built from
// the canonicalized template; the service may reject the registration outright
// via valid=false errors, or surface warnings.
//
// This example routes on the ValidateRequest.context oneof and, for the
// executor role, accepts a well-formed executor context (valid=true) and
// rejects a deliberately-bad one (valid=false with one ValidationFinding).
//
// It is NOT a test double and NOT a deployable service. Copy this directory,
// rename the module in go.mod, and replace the body of Validate with your work.
package main

import (
	"context"
	"encoding/json"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Validation implements genv1.ValidationServer. Embedding the generated
// Unimplemented server keeps it forward-compatible: RPCs added to the protocol
// later never break this type.
type Validation struct {
	genv1.UnimplementedValidationServer
}

func newValidation() *Validation { return &Validation{} }

// Validate is a single request-response RPC; the request is self-describing via
// the role oneof, and implementations route on the oneof variant. A real
// service switches over every role it validates; this example demonstrates the
// executor and claim-producer arms and leaves the rest to the embedded
// Unimplemented defaults a copier fills in.
//
// rimsky treats a Validate that returns no errors as acceptance, so the empty
// ValidateResponse zero value is NOT a pass — Valid must be set explicitly.
func (v *Validation) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	if exec := req.GetExecutor(); exec != nil {
		return validateExecutor(exec), nil
	}
	if cp := req.GetClaimProducer(); cp != nil {
		return validateClaimProducer(cp), nil
	}
	// A role this example does not validate: accept it rather than silently
	// rejecting registration. A real service would route every role it owns.
	return &genv1.ValidateResponse{Valid: true}, nil
}

// validateExecutor checks the one statically-knowable property this example
// owns: the executor's attributes_schema must be parseable JSON (the canonical
// good shape is a JSON-schema object). A non-JSON blob is a hard registration
// error carrying a routable class, a human message, and a JSON-pointer path.
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

// Sentinel selector tokens used by the cross-stack proof in
// main_e2e_test.go to drive each validation outcome through the public
// registration surface. A real validator would key on producer-specific
// semantics (selector grammar, retention class, partition key), not magic
// strings — but the sentinels here let the proof prove the THREE outcomes
// (error blocks, warning passes, accept passes) deterministically without
// dragging in a domain-specific selector parser.
const (
	// SelectorTriggerError, present anywhere in a claim binding's
	// selector, surfaces an error-severity finding from the
	// claim-producer arm. Registration is refused with HTTP 400.
	SelectorTriggerError = "trigger-validation-error"
	// SelectorTriggerWarning, present anywhere in a claim binding's
	// selector, surfaces a warning-severity finding. Registration
	// succeeds; rimsky surfaces the warning on the response body
	// (and in the dry-run synthesis).
	SelectorTriggerWarning = "trigger-validation-warning"
)

// validateClaimProducer walks the claim bindings rimsky sends for a
// template-registration validation and surfaces a finding per the
// sentinel-selector convention above. Bindings without either sentinel
// pass cleanly — the proof's accept-case fixture sends a selector
// outside the sentinel grammar.
//
// Error and warning findings are mutually exclusive per binding, but a
// single response may carry both kinds (one per binding); the proof
// exercises each in isolation so its assertions stay one-shot. The
// `Path` field cites the per-binding JSON path
// `/claim_producer/claims/<i>/selector` so an operator reading the
// rejection body can locate the offending binding.
func validateClaimProducer(cp *genv1.ClaimProducerContext) *genv1.ValidateResponse {
	resp := &genv1.ValidateResponse{Valid: true}
	for i, b := range cp.GetClaims() {
		sel := b.GetSelector()
		if containsToken(sel, SelectorTriggerError) {
			resp.Valid = false
			resp.Errors = append(resp.Errors, &genv1.ValidationFinding{
				Class:   "selector_rejected_by_example_validator",
				Message: "selector carries the example validator's error-trigger sentinel",
				Path:    selectorPath(i),
			})
			continue
		}
		if containsToken(sel, SelectorTriggerWarning) {
			resp.Warnings = append(resp.Warnings, &genv1.ValidationFinding{
				Class:   "selector_flagged_by_example_validator",
				Message: "selector carries the example validator's warning-trigger sentinel",
				Path:    selectorPath(i),
			})
		}
	}
	return resp
}

// containsToken reports whether substr appears in s. Pulled out so the
// trigger-matching logic reads as a single intention; the example
// deliberately avoids a regex / grammar to stay copy-and-modify simple.
func containsToken(s, substr string) bool {
	if substr == "" || len(substr) > len(s) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// selectorPath builds the JSON-pointer to a specific binding's selector.
// Centralised so a future schema rename is one edit.
func selectorPath(i int) string {
	return "/claim_producer/claims/" + itoa(i) + "/selector"
}

// itoa is a no-allocation base-10 conversion for the small non-negative
// indices the binding loop produces. Avoids pulling strconv into the
// example's published Apache surface for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}
