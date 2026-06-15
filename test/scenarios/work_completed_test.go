// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executable acceptance proof for the work_started / work_completed
// ledger pairing: an operator or auditor reading the event log can pair
// every work_started event whose dispatch reaches a terminal with a
// work_completed event, so durations and did-everything-finish audits
// are computable from the ledger. Dispatches that never reach
// applyTerminal are paired where rimsky observes the loss: the
// heartbeat-loss sweep (SweepStaleHeartbeats) emits
// work_completed{terminal_kind:"abandoned"} for the zombie run it
// retires. A work_started whose supervisor died can therefore stay
// unpaired until the sweep's next pass reaps the run.
//
// Two runs are driven to terminal through the real stack (supervisor +
// stub executor + persistence): one completing successfully, one
// erroring through a give_up policy. For each, the proof reads the
// append-only event ledger and asserts exactly one work_started and
// exactly one work_completed for the dispatch, with matching node and
// dispatch identifiers and the terminal kind carried on the completion.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// assertWorkPair reads the node's event ledger and asserts the
// work_started / work_completed pairing the story promises: exactly one
// of each, node identifiers matching, the same dispatch_id on both
// payloads (the run identifier the pair is joined on), and the terminal
// kind stamped on the completion.
func assertWorkPair(t *testing.T, h *scenario.Harness, nodeID foundationshared.UUID, wantTerminalKind string) {
	t.Helper()
	// @deliberate: The work_completed append is a best-effort post-commit audit tx —
	// it can land moments after the state flip a state-based wait
	// observes. Anchor on the append-only ledger.
	completed := eventwait.WaitForEvent(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &nodeID, Kind: "work_completed"}, 15*time.Second)
	started := eventwait.Events(h.Ctx, t, h.Persist,
		eventwait.Matcher{NodeID: &nodeID, Kind: "work_started"})

	require.Len(t, started, 1, "expected exactly one work_started for the run")
	require.Len(t, completed, 1, "expected exactly one work_completed for the run")

	s, c := started[0], completed[0]
	require.NotNil(t, s.NodeID)
	require.NotNil(t, c.NodeID)
	require.Equal(t, nodeID, *s.NodeID, "work_started node id")
	require.Equal(t, nodeID, *c.NodeID, "work_completed node id")

	// @constraint: The pair joins on dispatch_id — the run identifier both halves
	// carry. Durations are computable from the two rows' timestamps.
	sDispatch, ok := s.Payload["dispatch_id"].(string)
	require.True(t, ok, "work_started payload must carry dispatch_id, got %v", s.Payload)
	require.NotEmpty(t, sDispatch)
	cDispatch, ok := c.Payload["dispatch_id"].(string)
	require.True(t, ok, "work_completed payload must carry dispatch_id, got %v", c.Payload)
	require.Equal(t, sDispatch, cDispatch, "work_started / work_completed must pair on dispatch_id")

	require.Equal(t, s.Payload["supervisor_id"], c.Payload["supervisor_id"],
		"work_started / work_completed must carry the same supervisor_id")
	require.Equal(t, wantTerminalKind, c.Payload["terminal_kind"],
		"work_completed must carry the terminal kind")
	require.False(t, c.OccurredAt.Before(s.OccurredAt),
		"work_completed must not precede its work_started twin")
}

// TestWorkCompletedPairsWorkStartedOnComplete drives a node to a
// successful terminal and proves the ledger pairing with terminal kind
// "complete".
func TestWorkCompletedPairsWorkStartedOnComplete(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-pairing", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	assertWorkPair(t, h, n.ID, "complete")
}

// TestWorkCompletedPairsWorkStartedOnErrored drives a node through a
// give_up error terminal and proves the pairing holds on the failure
// branch too, with terminal kind "errored".
func TestWorkCompletedPairsWorkStartedOnErrored(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "work-completed-pairing-errored", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {
						Policy: []node.PolicyAction{{Action: "give_up"}},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-work-completed-err", map[string]any{})

	n := h.FindNode(iid, "flaky")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 15*time.Second),
		"flaky did not reach failed")

	assertWorkPair(t, h, n.ID, "errored")
}
