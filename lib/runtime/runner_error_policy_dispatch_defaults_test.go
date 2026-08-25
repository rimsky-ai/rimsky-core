// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func driveRetryOnce(t *testing.T, tables persistence.Tables, args RunArgs, acq *acquisition) *policyDecision {
	t.Helper()
	acq.RetryDecision = nil
	if err := tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyErrorPolicyWithScratch(ctx, args, acq, "boom", "", nil, nil, nil, nil, tx)
		return err
	}); err != nil {
		t.Fatalf("applyErrorPolicyWithScratch: %v", err)
	}
	return acq.RetryDecision
}

func retryingNodeDef() *node.TemplateNodeDef {
	return &node.TemplateNodeDef{
		Type: "holder", Executor: "test-executor",
		ErrorTypes: map[string]node.ErrorTypePolicy{
			"boom": {Action: spec.ActionRetry},
		},
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestNodeWithNoRetryCapTakesTheDeploymentWideCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const deploymentCap = 3
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, retryingNodeDef())
	args.MaxRetriesDefault = deploymentCap

	for attempt := 1; attempt <= deploymentCap; attempt++ {
		decision := driveRetryOnce(t, tables, args, acq)
		if decision == nil || !decision.IsRetry() {
			t.Fatalf("attempt %d: the node did not retry under the deployment-wide cap of %d", attempt, deploymentCap)
		}
	}

	if decision := driveRetryOnce(t, tables, args, acq); decision != nil && decision.IsRetry() {
		t.Fatalf("a node with no max_retries retried past the deployment-wide cap of %d", deploymentCap)
	}
	var runRow *persistence.NodeRunForGate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runRow.State != cascade.NodeStateFailed {
		t.Fatalf("the run is %v after it exhausted the deployment-wide cap; want failed", runRow.State)
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestNodeRetryCapOverridesTheDeploymentWideCap(t *testing.T) {
	t.Parallel()

	const nodeCap = 1
	nodeDef := retryingNodeDef()
	nodeDef.MaxRetries = node.IntPtr(nodeCap)
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)
	args.MaxRetriesDefault = 9

	if decision := driveRetryOnce(t, tables, args, acq); decision == nil || !decision.IsRetry() {
		t.Fatalf("the node's own cap of %d allowed no retry", nodeCap)
	}
	if decision := driveRetryOnce(t, tables, args, acq); decision != nil && decision.IsRetry() {
		t.Fatalf("the node's own max_retries of %d did not override the deployment-wide cap", nodeCap)
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestNodeWithNoBackoffTakesTheWholeDeploymentWideBackoff(t *testing.T) {
	t.Parallel()

	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, retryingNodeDef())
	args.MaxRetriesDefault = 5
	args.RetryBackoffDefault = &spec.RetryBackoffConfig{
		Kind:        spec.BackoffExponential,
		BaseDelayMs: 100,
	}

	first := driveRetryOnce(t, tables, args, acq)
	if first == nil || first.DelayMs != 100 {
		t.Fatalf("first retry delay = %v, want 100ms from the deployment-wide backoff", first)
	}
	second := driveRetryOnce(t, tables, args, acq)
	if second == nil || second.DelayMs != 200 {
		t.Fatalf("second retry delay = %v, want 200ms; the deployment-wide backoff supplies its kind as well as its base delay", second)
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestNodeBackoffReplacesTheDeploymentWideBackoffWhole(t *testing.T) {
	t.Parallel()

	nodeDef := retryingNodeDef()
	nodeDef.RetryBackoff = &spec.RetryBackoffConfig{BaseDelayMs: 40}
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)
	args.MaxRetriesDefault = 5
	args.RetryBackoffDefault = &spec.RetryBackoffConfig{
		Kind:        spec.BackoffExponential,
		BaseDelayMs: 1000,
		MaxDelayMs:  60000,
	}

	first := driveRetryOnce(t, tables, args, acq)
	if first == nil || first.DelayMs != 40 {
		t.Fatalf("first retry delay = %v, want 40ms; the node's backoff replaces the default object", first)
	}
	second := driveRetryOnce(t, tables, args, acq)
	if second == nil || second.DelayMs != 40 {
		t.Fatalf("second retry delay = %v, want 40ms; the node's flat backoff keeps its own kind", second)
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestAsyncCallbackTerminalTakesTheDeploymentWideRetryDefaults(t *testing.T) {
	t.Parallel()

	const deploymentCap = 2
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, retryingNodeDef())

	callbacks := &CallbackServer{
		Registry:              NewCallbackRegistry(),
		Persist:               args.Persist,
		Queue:                 args.Queue,
		AdvisoryLocker:        args.AdvisoryLocker,
		ClaimHandles:          args.ClaimHandles,
		ClaimProducerRegistry: args.ClaimProducerRegistry,
		Clock:                 args.Clock,
		Logger:                args.Logger,
		SupervisorID:          args.SupervisorID,
		MaxRetriesDefault:     deploymentCap,
		RetryBackoffDefault: &spec.RetryBackoffConfig{
			Kind:        spec.BackoffExponential,
			BaseDelayMs: 100,
		},
	}
	asyncArgs := callbacks.runArgs(args.SupervisorID, args.ClaimProducerRegistry)

	first := driveRetryOnce(t, tables, asyncArgs, acq)
	if first == nil || !first.IsRetry() {
		t.Fatalf("an async executor reporting an error did not retry under the deployment-wide cap of %d", deploymentCap)
	}
	if first.DelayMs != 100 {
		t.Fatalf("first retry delay = %d, want 100ms from the deployment-wide backoff", first.DelayMs)
	}
	second := driveRetryOnce(t, tables, asyncArgs, acq)
	if second == nil || second.DelayMs != 200 {
		t.Fatalf("second retry delay = %v, want 200ms; the async path takes the deployment-wide backoff whole", second)
	}
	if third := driveRetryOnce(t, tables, asyncArgs, acq); third != nil && third.IsRetry() {
		t.Fatalf("the async path retried past the deployment-wide cap of %d", deploymentCap)
	}
}
