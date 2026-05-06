// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 43 — acquire_pass_invalidate_emit.
//
// Per spec §3.5, the handler.invalidate emit fires unconditionally when
// the handler runs — orthogonal to whether the executor was invoked.
//
// Two-node template: worker has on_acquire_unavailable:
// { resolve: pass, invalidate: { targets: [monitor], frame: next } }.
// The producer returns Unavailable. Worker passes WITHOUT invoking the
// executor; monitor must still receive the invalidate emit.
//
// Determinism: instead of counting `work_completed` events on monitor
// (racy: monitor's initial scheduler-tick dispatch + the
// invalidate-emit-driven dispatch can compress depending on tick
// timing, and the on_acquire_unavailable handler may re-fire across
// scheduler ticks producing extra invalidates), this test asserts the
// `message_emitted` audit event keyed on (target=monitor, type=invalidate)
// — which records every emit regardless of whether monitor's actual
// runs compress. The audit-event row is the spec-level guarantee
// (handler.invalidate emit is recorded); it is what an operator
// inspecting the dashboard would see, and it is unaffected by tick
// timing.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	"github.com/fallguy/rimsky/stores/common/action"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
)

func TestAcquirePassInvalidateEmit(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
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
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "should-not-run")
	h.Stub.WhenType("monitor").Complete(map[string]any{"m": 1}, true, "monitored")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "acquire-pass-invalidate-emit", Version: "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					OnAcquireUnavailable: &node.OnAcquireUnavailableHandler{
						Resolve: node.ResolvePass,
						Invalidate: &node.HandlerInvalidate{
							Targets: []string{"monitor"},
							Frame:   node.FrameNext,
						},
					},
				},
				scenario.WithStores(scenario.WriteClaimRef("queue-store", "@queue")),
			),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "monitor",
				Executor: "stub",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-acq-pass-emit", map[string]any{})

	worker := h.FindNode(iid, "worker")
	monitor := h.FindNode(iid, "monitor")
	require.NotNil(t, worker)
	require.NotNil(t, monitor)

	// Worker should pass.
	require.True(t, waitForLastOutcome(t, h, worker.ID, shared.LastOutcomePassed, 30*time.Second),
		"worker should record last_outcome=passed")

	// Deterministic assertion: the handler.invalidate emit produces a
	// `message_emitted` row keyed on the target node, with payload
	// `type=invalidate`. This row is written synchronously in the same
	// tx as the emit and is unaffected by the timing of monitor's
	// downstream re-runs. We require AT LEAST ONE such row (the
	// handler may fire repeatedly across scheduler ticks while the
	// producer remains Unavailable; the spec property is "the emit is
	// recorded", not "exactly once").
	deadline := time.Now().Add(30 * time.Second)
	var emitCount int
	for time.Now().Before(deadline) {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_events
			 WHERE node_id = $1
			   AND kind = 'message_emitted'
			   AND payload->>'type' = 'invalidate'`,
			monitor.ID,
		).Scan(&emitCount))
		if emitCount >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.GreaterOrEqual(t, emitCount, 1,
		"handler.invalidate must record at least one message_emitted on the target; got %d", emitCount)

	// Sanity: monitor actually ran (executor was invoked) at least once
	// in response to the emit. This is the user-observable consequence
	// of the spec property; it is racy on count but deterministic on
	// "≥ 1" given the 30s window.
	require.True(t, waitForEventCount(t, h, monitor.ID, "work_completed", 1, 30*time.Second),
		"monitor must run at least once in response to the handler.invalidate emit")

	// Worker's executor must NOT have been invoked. Filter the stub's
	// observed list to worker only (monitor uses the same stub).
	var workerObserved int
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			workerObserved++
		}
	}
	require.Equal(t, 0, workerObserved,
		"worker's executor must not be invoked when on_acquire_unavailable: pass fires")

	// Worker is fresh.
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, shared.NodeStateFresh, wRow.State)
}
