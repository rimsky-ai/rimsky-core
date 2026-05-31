// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package validation is the importable form of the Validation mix-in
// conformance suite. The `rimsky conformance validation` subcommand is a
// thin wrapper that dials the endpoint and invokes Run; tests can
// invoke Run directly against an in-process or testcontainers-hosted
// service that advertises the validation protocol.
//
// The roles supported by the suite mirror the proto's role oneof:
// executor, claim_producer, lifecycle_subscriber, sensor.

package validation

import (
	"context"
	"encoding/json"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// CheckResult is one row of conformance output. Err nil on pass.
type CheckResult struct {
	Name string
	Err  error
}

// Run drives the conformance suite for the given role. Each role has
// its own happy-path and malformed-input probes; the dispatcher
// routes on the role string.
func Run(ctx context.Context, c genv1.ValidationClient, role string) []CheckResult {
	results := make([]CheckResult, 0, 4)
	switch role {
	case "executor":
		results = append(results, checkExecutorHappy(ctx, c))
		results = append(results, checkExecutorMalformedAttributesSchema(ctx, c))
		results = append(results, checkExecutorMissingContext(ctx, c))
		results = append(results, checkUnknownRole(ctx, c))
	case "claim_producer":
		results = append(results, checkClaimProducerHappy(ctx, c))
		results = append(results, checkUnknownRole(ctx, c))
	case "lifecycle_subscriber":
		results = append(results, checkLifecycleSubscriberHappy(ctx, c))
		results = append(results, checkUnknownRole(ctx, c))
	case "sensor":
		results = append(results, checkSensorHappy(ctx, c))
		results = append(results, checkUnknownRole(ctx, c))
	default:
		results = append(results, CheckResult{
			Name: "RoleDispatch",
			Err:  fmt.Errorf("unsupported --role %q; must be one of: executor, claim_producer, lifecycle_subscriber, sensor", role),
		})
	}
	return results
}

// checkExecutorHappy fires Validate against a well-shaped executor
// context. Pins shape only.
func checkExecutorHappy(ctx context.Context, c genv1.ValidationClient) CheckResult {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"checks": map[string]any{
				"type":    "array",
				"default": []map[string]any{{"kind": "no_nulls", "config": map[string]any{"field": "id"}}},
			},
		},
	})
	req := &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "conformance-executor-node",
			AttributesSchema: schema,
			ClaimAliases:     []string{"input"},
		}},
	}
	resp, err := c.Validate(ctx, req)
	if err != nil {
		return CheckResult{Name: "ExecutorHappy", Err: err}
	}
	if resp == nil {
		return CheckResult{Name: "ExecutorHappy", Err: fmt.Errorf("Validate returned nil response without error")}
	}
	return CheckResult{Name: "ExecutorHappy"}
}

// checkExecutorMalformedAttributesSchema fires Validate with malformed
// JSON in the attributes_schema and asserts `valid` is false with at
// least one error finding.
func checkExecutorMalformedAttributesSchema(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{
		Role: "executor",
		Context: &genv1.ValidateRequest_Executor{Executor: &genv1.ExecutorContext{
			NodeAlias:        "conformance-executor-malformed",
			AttributesSchema: []byte("this is not json {"),
		}},
	}
	resp, err := c.Validate(ctx, req)
	if err != nil {
		return CheckResult{Name: "ExecutorMalformedAttributesSchema", Err: err}
	}
	if resp.GetValid() {
		return CheckResult{
			Name: "ExecutorMalformedAttributesSchema",
			Err:  fmt.Errorf("validator returned Valid=true for malformed JSON attributes_schema; expected an error finding"),
		}
	}
	if len(resp.GetErrors()) == 0 {
		return CheckResult{
			Name: "ExecutorMalformedAttributesSchema",
			Err:  fmt.Errorf("validator returned Valid=false but zero error findings"),
		}
	}
	for i, f := range resp.GetErrors() {
		if f.GetClass() == "" {
			return CheckResult{
				Name: "ExecutorMalformedAttributesSchema",
				Err:  fmt.Errorf("errors[%d].class empty (validator must surface a stable discriminator)", i),
			}
		}
	}
	return CheckResult{Name: "ExecutorMalformedAttributesSchema"}
}

// checkExecutorMissingContext fires Validate with role="executor" but
// no executor context. The check asserts the validator surfaces an
// error rather than crashing.
func checkExecutorMissingContext(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{Role: "executor"}
	resp, err := c.Validate(ctx, req)
	if err != nil {
		return CheckResult{Name: "ExecutorMissingContext", Err: err}
	}
	if resp.GetValid() {
		return CheckResult{
			Name: "ExecutorMissingContext",
			Err:  fmt.Errorf("validator returned Valid=true with no executor context; expected an error"),
		}
	}
	return CheckResult{Name: "ExecutorMissingContext"}
}

// checkUnknownRole fires Validate with an unknown role string. The
// validator may surface an error finding OR return a gRPC error; the
// check tolerates either path. It must not return Valid=true.
func checkUnknownRole(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{Role: "unknown-role-conformance"}
	resp, err := c.Validate(ctx, req)
	if err != nil {
		// Acceptable: gRPC error.
		return CheckResult{Name: "UnknownRole"}
	}
	if resp.GetValid() {
		return CheckResult{
			Name: "UnknownRole",
			Err:  fmt.Errorf("validator returned Valid=true for unknown role; expected rejection"),
		}
	}
	return CheckResult{Name: "UnknownRole"}
}

// checkClaimProducerHappy fires Validate against a well-shaped
// claim_producer context. Pins RPC shape.
func checkClaimProducerHappy(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{
		Role: "claim_producer",
		Context: &genv1.ValidateRequest_ClaimProducer{ClaimProducer: &genv1.ClaimProducerContext{
			ProducerName: "conformance-producer",
			Claims: []*genv1.ClaimBinding{{
				NodeAlias:  "conformance-node",
				ClaimAlias: "input",
				Selector:   "conformance/selector",
				Intent:     "r",
				Lifetime:   "subgraph",
			}},
		}},
	}
	if _, err := c.Validate(ctx, req); err != nil {
		return CheckResult{Name: "ClaimProducerHappy", Err: err}
	}
	return CheckResult{Name: "ClaimProducerHappy"}
}

// checkLifecycleSubscriberHappy fires Validate against a well-shaped
// lifecycle_subscriber context.
func checkLifecycleSubscriberHappy(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{
		Role: "lifecycle_subscriber",
		Context: &genv1.ValidateRequest_LifecycleSubscriber{LifecycleSubscriber: &genv1.LifecycleSubscriberContext{
			SubscriberName: "conformance-subscriber",
			TemplateId:     "sha256-aaaa",
		}},
	}
	if _, err := c.Validate(ctx, req); err != nil {
		return CheckResult{Name: "LifecycleSubscriberHappy", Err: err}
	}
	return CheckResult{Name: "LifecycleSubscriberHappy"}
}

// checkSensorHappy fires Validate against a well-shaped sensor
// context.
func checkSensorHappy(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{
		Role: "sensor",
		Context: &genv1.ValidateRequest_Sensor{Sensor: &genv1.SensorContext{
			SensorName:     "conformance-sensor",
			Kind:           "cron",
			ResolvedConfig: []byte(`{"cron":"*/5 * * * *"}`),
		}},
	}
	if _, err := c.Validate(ctx, req); err != nil {
		return CheckResult{Name: "SensorHappy", Err: err}
	}
	return CheckResult{Name: "SensorHappy"}
}
