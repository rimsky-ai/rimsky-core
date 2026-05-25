// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-4 test: pre-dispatch acquisition failure routes through the
// operator's error_types: chain via synthetic class
// "acquire/unavailable". Replaces the pre-2026-05-23
// on_acquire_unavailable lifecycle-handler slot. Per spec
// .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-
// design.md §ErrorPolicy.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/control/config"
	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
	"github.com/fallguyconsulting/rimsky/stores/common/action"
	stubstore "github.com/fallguyconsulting/rimsky/stores/stub/store"
	stubfixture "github.com/fallguyconsulting/rimsky/stores/stub/testfixture"
)

// TestAcquireUnavailable_RoutesViaErrorTypes confirms that
// pre-dispatch acquisition failure routes through the operator's
// `error_types: { "acquire/unavailable": ... }` chain. With a
// give_up-terminating chain (after a retry) the node lands in failed.
func TestAcquireUnavailable_RoutesViaErrorTypes(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// Empty queue — Open returns Unavailable.
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-error-types", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					// Synthetic class "acquire/unavailable" — per the
					// 2026-05-23 reshape, pre-dispatch acquisition
					// failure routes through this key. give_up drives
					// the node into failed.
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-err-types", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// give_up drives the node to failed.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should land in failed via error_types: { acquire/unavailable: give_up }")

	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.NotNil(t, wRow.SettlingSignalType)
	require.Contains(t, *wRow.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")

	// Audit-log assertion: the canonical
	// `terminal/error/acquire/unavailable` signal row must land on
	// `rimsky_events` so subscribers wildcard-matching
	// `terminal/error/*` can catch acquire failures alongside
	// executor errors. Per concept:signal and the OnError /
	// signalaudit.EmitSignal path.
	require.True(t,
		h.WaitForEventKind(worker.ID, "terminal/error/acquire/unavailable", 5*time.Second),
		"OnError must emit canonical terminal/error/acquire/unavailable on the audit log")

	// Executor must not have been invoked.
	require.Empty(t, h.Stub.Observed(),
		"executor must not be invoked when acquire/unavailable routes to give_up")
}

// TestAcquireUnavailable_NoPolicyFailsFast confirms the intentional
// post-2026-05-23 behavior change: when no `error_types:
// { "acquire/unavailable": ... }` is declared, the node fails-fast
// (give_up("unknown_error_class")) rather than the pre-Pass-4 implicit
// retry behavior.
func TestAcquireUnavailable_NoPolicyFailsFast(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				// Empty queue — Open returns Unavailable.
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acq-unavail-no-policy", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					// No error_types: { acquire/unavailable: ... } — the
					// default is fail-fast. The validator surfaces a
					// warning for this case (see
					// graph/node/template_validator.go::
					// validateAcquireUnavailablePolicyAdvised).
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-unavail-no-policy", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Absent policy → give_up("unknown_error_class") → failed.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should fail-fast when no acquire/unavailable policy is declared")
}
