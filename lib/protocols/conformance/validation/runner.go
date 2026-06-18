// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package validation

import (
	"context"
	"encoding/json"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type CheckResult struct {
	Name string
	Err  error
}

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

func checkUnknownRole(ctx context.Context, c genv1.ValidationClient) CheckResult {
	req := &genv1.ValidateRequest{Role: "unknown-role-conformance"}
	resp, err := c.Validate(ctx, req)
	if err != nil {
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
