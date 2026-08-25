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

func driveInfraErrorOnce(t *testing.T, tables persistence.Tables, args RunArgs, acq *acquisition) spec.EvaluatorState {
	t.Helper()
	ctx := context.Background()
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalInfraError(ctx, args, acq, "dial_boom", nil, nil, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalInfraError: %v", err)
	}
	var state spec.EvaluatorState
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		s, err := tables.Nodes().GetRunEvaluatorState(ctx, acq.NodeRunID, tx)
		state = s
		return err
	}); err != nil {
		t.Fatalf("GetRunEvaluatorState: %v", err)
	}
	return state
}

const wantDefaultInfraRetryCap = 10

func TestApplyTerminalInfraError_SkipsOperatorPolicyAndUsesDefaultRetryCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if defaultInfraRetryCap != wantDefaultInfraRetryCap {
		t.Fatalf("defaultInfraRetryCap = %d, want %d (the supervisor default infra retry cap)",
			defaultInfraRetryCap, wantDefaultInfraRetryCap)
	}

	giveUpNow := node.ErrorTypePolicy{Action: spec.ActionGiveUp}
	nodeDef := &node.TemplateNodeDef{
		Type: "holder", Executor: "test-executor",
		ErrorTypes: map[string]node.ErrorTypePolicy{
			"dial_boom": giveUpNow,
		},
	}
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)

	for attempt := 1; attempt <= wantDefaultInfraRetryCap; attempt++ {
		state := driveInfraErrorOnce(t, tables, args, acq)
		if state.RetryCounter != attempt {
			t.Fatalf("attempt %d: RetryCounter = %d, want %d", attempt, state.RetryCounter, attempt)
		}
		var runRow *persistence.NodeRunForGate
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
			runRow = r
			return err
		}); err != nil {
			t.Fatalf("load run: %v", err)
		}
		if runRow.State != cascade.NodeStateRunning {
			t.Fatalf("attempt %d: run state = %v, want %v -- infra errors must retry through "+
				"the default cap of %d regardless of the node's ErrorTypes config (which maps "+
				"this class straight to give_up); an operator error policy consulted here would "+
				"have failed the node on attempt 1", attempt, runRow.State, cascade.NodeStateRunning, wantDefaultInfraRetryCap)
		}
	}

	var runRow *persistence.NodeRunForGate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalInfraError(ctx, args, acq, "dial_boom", nil, nil, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalInfraError (give-up attempt): %v", err)
	}
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runRow.State != cascade.NodeStateFailed {
		t.Fatalf("after %d retries, one more infra error must give up (state=failed); got %v",
			wantDefaultInfraRetryCap, runRow.State)
	}
}

func TestApplyTerminalInfraError_NodeMaxRetriesOverridesDefaultCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const nodeCap = 2
	nodeDef := &node.TemplateNodeDef{
		Type: "holder", Executor: "test-executor",
		MaxRetries: node.IntPtr(nodeCap),
	}
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)

	for attempt := 1; attempt <= nodeCap; attempt++ {
		driveInfraErrorOnce(t, tables, args, acq)
		var runRow *persistence.NodeRunForGate
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
			runRow = r
			return err
		}); err != nil {
			t.Fatalf("load run: %v", err)
		}
		if runRow.State != cascade.NodeStateRunning {
			t.Fatalf("attempt %d of node MaxRetries=%d: run state = %v, want %v",
				attempt, nodeCap, runRow.State, cascade.NodeStateRunning)
		}
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalInfraError(ctx, args, acq, "dial_boom", nil, nil, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalInfraError (give-up attempt): %v", err)
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
		t.Fatalf("node MaxRetries=%d must cap retries below the default of %d: after %d retries "+
			"one more infra error must give up (state=failed); got %v",
			nodeCap, wantDefaultInfraRetryCap, nodeCap, runRow.State)
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestApplyTerminalInfraError_DeploymentMaxRetriesDefaultCapsRetriesLikeANodesOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const deploymentCap = 2
	nodeDef := &node.TemplateNodeDef{Type: "holder", Executor: "test-executor"}
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)
	args.MaxRetriesDefault = deploymentCap

	for attempt := 1; attempt <= deploymentCap; attempt++ {
		driveInfraErrorOnce(t, tables, args, acq)
		var runRow *persistence.NodeRunForGate
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
			runRow = r
			return err
		}); err != nil {
			t.Fatalf("load run: %v", err)
		}
		if runRow.State != cascade.NodeStateRunning {
			t.Fatalf("attempt %d of dispatch_defaults.max_retries=%d: run state = %v, want %v",
				attempt, deploymentCap, runRow.State, cascade.NodeStateRunning)
		}
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalInfraError(ctx, args, acq, "dial_boom", nil, nil, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalInfraError (give-up attempt): %v", err)
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
		t.Fatalf("dispatch_defaults.max_retries=%d must cap infra retries exactly as a node's own "+
			"max_retries does: after %d retries one more infra error must give up (state=failed); got %v",
			deploymentCap, deploymentCap, runRow.State)
	}
}

// @decision: dispatch-defaults-cover-every-node-timing-key
func TestApplyTerminalInfraError_NoDeploymentDefaultKeepsRimskysOwnCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	nodeDef := &node.TemplateNodeDef{Type: "holder", Executor: "test-executor"}
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)
	if args.MaxRetriesDefault != 0 {
		t.Fatalf("fixture MaxRetriesDefault = %d, want 0 (a deployment that sets no default)", args.MaxRetriesDefault)
	}

	for attempt := 1; attempt <= wantDefaultInfraRetryCap; attempt++ {
		driveInfraErrorOnce(t, tables, args, acq)
	}
	var runRow *persistence.NodeRunForGate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if runRow.State != cascade.NodeStateRunning {
		t.Fatalf("with no node and no deployment max_retries, infra errors retry to rimsky's own cap of %d; got %v",
			wantDefaultInfraRetryCap, runRow.State)
	}
}

// @concept: sub-graph
func TestApplyTerminalInfraError_SubgraphExitNodeStillRetriesAndGivesUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const nodeCap = 2
	nodeDef := &node.TemplateNodeDef{
		Type: "holder", Executor: "test-executor",
		MaxRetries:     node.IntPtr(nodeCap),
		IsSubgraphExit: true,
	}
	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nodeDef)

	for attempt := 1; attempt <= nodeCap; attempt++ {
		driveInfraErrorOnce(t, tables, args, acq)
		var runRow *persistence.NodeRunForGate
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
			runRow = r
			return err
		}); err != nil {
			t.Fatalf("load run: %v", err)
		}
		if runRow.State != cascade.NodeStateRunning {
			t.Fatalf("attempt %d: subgraph-exit run state = %v, want %v (infra retry must still run "+
				"for subgraph-exit nodes)", attempt, runRow.State, cascade.NodeStateRunning)
		}
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalInfraError(ctx, args, acq, "dial_boom", nil, nil, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalInfraError (give-up attempt): %v", err)
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
		t.Fatalf("a subgraph-exit run whose infra retries are exhausted must give up to failed like any "+
			"other node, not strand in running until the orphan reaper; got state=%v", runRow.State)
	}
}
