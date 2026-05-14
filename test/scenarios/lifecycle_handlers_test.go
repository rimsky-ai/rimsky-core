// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle-handler scenario tests covering the three declarative slots
// (on_acquire_unavailable, on_executor_complete, on_executor_errored),
// per-emit frame discipline, and the last_outcome cascade gate. Per
// the reactive-loops + lifecycle-handlers spec at
// .ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md.
package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
)

// TestAlwaysPropagateResolution covers Task 31. With on_executor_complete:
// {resolve: always_propagate}, a Complete{changed:false} terminal must
// still cascade — last_outcome=fresh_changed regardless of t.Changed.
func TestAlwaysPropagateResolution(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, false, "noop")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "always-propagate", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:               "a",
				Executor:           "stub",
				OnExecutorComplete: &node.OnExecutorCompleteHandler{Resolve: node.ResolveAlwaysPropagate},
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "b",
				Executor:     "stub",
				Dependencies: []string{"a"},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-always", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	// a commits with changed=false — but always_propagate forces the
	// cascade gate to fire; b should be re-run.
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a did not reach fresh")
	if !h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second) {
		var bRowDbg *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
			bRowDbg = r
			return err
		})
		t.Fatalf("b did not reach fresh — always_propagate should have cascaded despite changed=false; b state=%v frame_id=%v", bRowDbg.State, bRowDbg.FrameID)
	}

	// Verify a's last_outcome.
	var aRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		aRow = r
		return err
	}))
	require.Equal(t, cascade.LastOutcomeFreshChanged, aRow.LastOutcome,
		"always_propagate must record last_outcome=fresh_changed")
}

// TestNeverPropagateResolution covers Task 32. With on_executor_complete:
// {resolve: never_propagate}, a Complete{changed:true} terminal must
// NOT cascade.
func TestNeverPropagateResolution(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a-changed")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "never-propagate", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:               "a",
				Executor:           "stub",
				OnExecutorComplete: &node.OnExecutorCompleteHandler{Resolve: node.ResolveNeverPropagate},
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "b",
				Executor:     "stub",
				Dependencies: []string{"a"},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-never", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	// a should reach fresh; b should stay fresh (never cascaded).
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a did not reach fresh")
	// Give the system a beat to confirm b doesn't run.
	time.Sleep(2 * time.Second)

	var aRow, bRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		if err != nil {
			return err
		}
		aRow = ra
		rb, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
		bRow = rb
		return err
	}))
	require.Equal(t, cascade.LastOutcomeFreshUnchanged, aRow.LastOutcome,
		"never_propagate must record last_outcome=fresh_unchanged")
	require.Equal(t, cascade.NodeStateFresh, bRow.State,
		"b should remain fresh — never_propagate must not cascade")
}

// TestFreshUnchangedDoesNotCascade covers Task 34. Default by_changed
// resolution with t.Changed=false must NOT cascade. Same as today's
// no_op_commit test, but explicit on the column-based gate.
func TestFreshUnchangedDoesNotCascade(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, false, "noop")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fresh-unchanged-no-cascade", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "b",
				Executor:     "stub",
				Dependencies: []string{"a"},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-fucnc", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a did not reach fresh")
	time.Sleep(2 * time.Second)

	var aRow, bRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		if err != nil {
			return err
		}
		aRow = ra
		rb, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
		bRow = rb
		return err
	}))
	require.Equal(t, cascade.LastOutcomeFreshUnchanged, aRow.LastOutcome,
		"by_changed + changed=false must record last_outcome=fresh_unchanged")
	require.Equal(t, cascade.NodeStateFresh, bRow.State,
		"b should remain fresh on a no-op commit")
}

// TestFailedUpstreamFreezesDownstream covers Task 36. A failed upstream
// node freezes downstream — they don't fire (today's behavior; the
// last_outcome=failed column doesn't change this).
func TestFailedUpstreamFreezesDownstream(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Error("fatal", map[string]any{"why": "boom"})
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "failed-freezes", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"fatal": {Policy: []node.PolicyAction{{Action: "give_up"}}},
				},
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "b",
				Executor:     "stub",
				Dependencies: []string{"a"},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-fail-freeze", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFailed, 30*time.Second),
		"a should land in failed")
	time.Sleep(2 * time.Second)

	var aRow, bRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		if err != nil {
			return err
		}
		aRow = ra
		rb, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
		bRow = rb
		return err
	}))
	require.Equal(t, cascade.LastOutcomeFailed, aRow.LastOutcome,
		"give_up should record last_outcome=failed")
	require.NotEqual(t, cascade.NodeStateRunning, bRow.State,
		"b should not run while upstream is failed")
}

// TestExecutorBlockedPassResolution covers Task 37 (migrated post-E.10).
// With on_executor_errored: {resolve: pass}, a stub-emitted
// Error{executor_blocked} lands the node in fresh+passed without
// error_types routing. (Pre-2026-05-12 this used on_executor_blocked,
// which collapsed into on_executor_errored under spec E.2 / E.10.)
func TestExecutorBlockedPassResolution(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("executor_blocked", map[string]any{
		"reason": "blocked_class",
		"why":    "stub-blocked",
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "blocked-pass", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:              "worker",
				Executor:          "stub",
				OnExecutorErrored: &node.OnExecutorTerminalHandler{Resolve: node.ResolvePass},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-blocked-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait for the handler_pass state_transition event (the resolve=pass
	// path emits this). Then verify the row state and last_outcome.
	require.True(t, waitForLastOutcome(t, h, worker.ID, cascade.LastOutcomePassed, 30*time.Second),
		"worker should record last_outcome=passed under on_executor_errored: pass (post-E.10 — blocked collapsed)")
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, wRow.State, "worker should be fresh after resolve=pass")
}

// TestExecutorErroredPassResolution covers Task 38. Same as Task 37 but
// for the Errored terminal.
func TestExecutorErroredPassResolution(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("any_class", map[string]any{"why": "stub-err"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "errored-pass", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:              "worker",
				Executor:          "stub",
				OnExecutorErrored: &node.OnExecutorTerminalHandler{Resolve: node.ResolvePass},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-errored-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, waitForLastOutcome(t, h, worker.ID, cascade.LastOutcomePassed, 30*time.Second),
		"worker should record last_outcome=passed under on_executor_errored: pass")
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, wRow.State, "worker should be fresh after resolve=pass")
}

// TestOperatorInvalidateTargetOnly covers Task 35. Invalidating A in
// chain A → B → C marks only A stale; B and C stay fresh. Cascade
// happens lazily when A's commit propagates.
func TestOperatorInvalidateTargetOnly(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: false})
	h.Stub.WhenType("a").Success(map[string]any{}, true, "a-init")
	h.Stub.WhenType("b").Success(map[string]any{}, true, "b")
	h.Stub.WhenType("c").Success(map[string]any{}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "operator-invalidate-target", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "b", Executor: "stub", Dependencies: []string{"a"}}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "c", Executor: "stub", Dependencies: []string{"b"}}),
		},
	})
	iid := h.CreateInstance(tid, "ck-op-inv-target", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")

	require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second), "c initial")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second), "b initial")
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second), "a initial")

	// Switch a's stub to changed=false so the cascade-on-commit gate
	// stops at A. With default by_changed, B and C should stay fresh.
	h.Stub.WhenType("a").Success(map[string]any{}, false, "a-noop")

	// Operator invalidate against A.
	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(h.ControlBase+"/nodes/"+a.ID.String()+"/invalidate", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()

	// A should re-run and reach fresh (with last_outcome=fresh_unchanged).
	require.True(t, waitForLastOutcome(t, h, a.ID, cascade.LastOutcomeFreshUnchanged, 30*time.Second),
		"a should record last_outcome=fresh_unchanged on the no-op rerun")
	// Give the system a beat to confirm B/C don't run.
	time.Sleep(2 * time.Second)
	var bRow, cRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rb, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
		if err != nil {
			return err
		}
		bRow = rb
		rc, err := h.Persist.Nodes().Get(h.Ctx, c.ID, tx)
		cRow = rc
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, bRow.State, "b should stay fresh on a no-op rerun")
	require.Equal(t, cascade.NodeStateFresh, cRow.State, "c should stay fresh on a no-op rerun")
}

// waitForLastOutcome polls the node row until last_outcome matches.
func waitForLastOutcome(t *testing.T, h *scenario.Harness, nodeID shared.UUID, want cascade.LastOutcome, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var row *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, nodeID, tx)
			row = r
			return err
		})
		if row != nil && row.LastOutcome == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// TestPureCascadeOutcomeColumn covers Task 33 (subset). A pure-cascade
// transition (stale → fresh via ReasonPureCascade) must record
// last_outcome=pure_cascade.
func TestPureCascadeOutcomeColumn(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pure-cascade", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "p",
				Dependencies: []string{"a"},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-pure-cascade", map[string]any{})

	a := h.FindNode(iid, "a")
	p := h.FindNode(iid, "p")
	require.NotNil(t, a)
	require.NotNil(t, p)

	require.True(t, h.WaitForNodeState(p.ID, cascade.NodeStateFresh, 30*time.Second),
		"pure-cascade node p did not reach fresh")

	var pRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, p.ID, tx)
		pRow = r
		return err
	}))
	require.Equal(t, cascade.LastOutcomePureCascade, pRow.LastOutcome,
		"pure_cascade transition should record last_outcome=pure_cascade")
}
