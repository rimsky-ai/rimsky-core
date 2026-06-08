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
// executor arm and leaves the rest to the embedded Unimplemented defaults a
// copier fills in.
//
// rimsky treats a Validate that returns no errors as acceptance, so the empty
// ValidateResponse zero value is NOT a pass — Valid must be set explicitly.
func (v *Validation) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	if exec := req.GetExecutor(); exec != nil {
		return validateExecutor(exec), nil
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
