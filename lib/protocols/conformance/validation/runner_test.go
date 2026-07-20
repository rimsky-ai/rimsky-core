// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package validation

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakeValidationClient struct {
	respond func(role string) *genv1.ValidateResponse
}

func (f *fakeValidationClient) Validate(_ context.Context, req *genv1.ValidateRequest, _ ...grpc.CallOption) (*genv1.ValidateResponse, error) {
	return f.respond(req.GetRole()), nil
}

func alwaysValid(string) *genv1.ValidateResponse {
	return &genv1.ValidateResponse{Valid: true}
}

func findValidationResult(t *testing.T, results []CheckResult, name string) CheckResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Name)
	}
	t.Fatalf("result set missing %q (have: %v)", name, names)
	return CheckResult{}
}

func TestClaimProducerHappy_Honest_Passes(t *testing.T) {
	c := &fakeValidationClient{respond: alwaysValid}
	results := Run(context.Background(), c, "claim_producer")
	r := findValidationResult(t, results, "ClaimProducerHappy")
	if r.Err != nil {
		t.Errorf("ClaimProducerHappy expected PASS for an honest Valid=true response, got Err: %v", r.Err)
	}
}

func TestClaimProducerHappy_UnsupportedRoleRejection_Fails(t *testing.T) {
	c := &fakeValidationClient{respond: func(role string) *genv1.ValidateResponse {
		return &genv1.ValidateResponse{
			Valid:  false,
			Errors: []*genv1.ValidationFinding{{Class: "unsupported_role", Message: "role not supported"}},
		}
	}}
	results := Run(context.Background(), c, "claim_producer")
	r := findValidationResult(t, results, "ClaimProducerHappy")
	if r.Err == nil {
		t.Error("ClaimProducerHappy expected non-nil Err when the validator rejects the role as unsupported, got PASS")
	}
}

func TestLifecycleSubscriberHappy_UnsupportedRoleRejection_Fails(t *testing.T) {
	c := &fakeValidationClient{respond: func(role string) *genv1.ValidateResponse {
		return &genv1.ValidateResponse{Valid: false}
	}}
	results := Run(context.Background(), c, "lifecycle_subscriber")
	r := findValidationResult(t, results, "LifecycleSubscriberHappy")
	if r.Err == nil {
		t.Error("LifecycleSubscriberHappy expected non-nil Err when the validator returns Valid=false, got PASS")
	}
}

func TestPublisherHappy_UnsupportedRoleRejection_Fails(t *testing.T) {
	c := &fakeValidationClient{respond: func(role string) *genv1.ValidateResponse {
		return &genv1.ValidateResponse{Valid: false}
	}}
	results := Run(context.Background(), c, "publisher")
	r := findValidationResult(t, results, "PublisherHappy")
	if r.Err == nil {
		t.Error("PublisherHappy expected non-nil Err when the validator returns Valid=false, got PASS")
	}
}

func TestPublisherHappy_NilResponse_Fails(t *testing.T) {
	c := &fakeValidationClient{respond: func(role string) *genv1.ValidateResponse {
		return nil
	}}
	results := Run(context.Background(), c, "publisher")
	r := findValidationResult(t, results, "PublisherHappy")
	if r.Err == nil {
		t.Error("PublisherHappy expected non-nil Err when Validate returns a nil response without error, got PASS")
	}
}

func TestExecutorHappy_Honest_Passes(t *testing.T) {
	c := &fakeValidationClient{respond: alwaysValid}
	results := Run(context.Background(), c, "executor")
	r := findValidationResult(t, results, "ExecutorHappy")
	if r.Err != nil {
		t.Errorf("ExecutorHappy expected PASS for an honest Valid=true response, got Err: %v", r.Err)
	}
}

func TestExecutorHappy_ValidFalse_Fails(t *testing.T) {
	c := &fakeValidationClient{respond: func(role string) *genv1.ValidateResponse {
		return &genv1.ValidateResponse{Valid: false}
	}}
	results := Run(context.Background(), c, "executor")
	r := findValidationResult(t, results, "ExecutorHappy")
	if r.Err == nil {
		t.Error("ExecutorHappy expected non-nil Err when the validator returns Valid=false for a well-formed happy-path request, got PASS")
	}
}
