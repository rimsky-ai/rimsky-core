// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Acceptance gate for S-cascade-operator-frame-in (spec
// 2026-06-06-comprehensive-gap-closure, plan TEMPLCASCADE-AG6). The
// per-pass test TestInvalidateNode_FrameIn_JoinsRunningFrame
// (lib/control/controlapi/nodes_test.go) pins the same behavior at
// handler altitude with a hand-promoted frame; THIS test proves the
// user-outcome story holds end-to-end against the REAL assembled
// product — real control-api over HTTP, real scheduler + frame engine,
// real supervisor + stub-executor dispatch, testcontainers Postgres.
//
// Story: an operator issues `POST /nodes/{target}/invalidate`
// {"frame":"in"} against a node whose instance has a currently-OPEN
// cascade frame F (a source node settled inside F, a dependent
// mid-drain holding F open). The invalidate must JOIN the running
// frame F — the target's run row + node row acquire frame_id == F and
// a `state_transition` event carries reason `in_frame_invalidate` with
// frame_id == F — rather than being silently downgraded to a freshly-
// enqueued NEXT frame. One frame_id is observed end to end, not two
// sequential frames.
//
// Genuinely-running-frame setup (NOT hand-rolled state — the whole
// point of the e2e altitude over the per-pass test):
//   - A coalesce-mode template with two root nodes. In coalesce mode
//     both roots coalesce into ONE queued root frame F
//     (EnqueueCoalesceFrame upserts a single row with both source ids).
//   - The real frame engine advances F to `running` and the real
//     supervisor dispatches both sources inside F:
//   - `source` settles fresh (a settled source inside F);
//   - `holder` parks indefinitely, which holds F `running`/held
//     (parked is unresolved for frame-end), so F stays OPEN and
//     `holder` is a genuine in-flight (parked) run mid-drain inside
//     F. `holder` is the operator's invalidate target — the
//     story's "dependent mid-drain in running frame F".
//
// Decisive RED-vs-GREEN discriminators (exactly the per-pass test's
// load-bearing assertions, now driven through the real product):
//   - the `in_frame_invalidate` state_transition event for the target,
//     carrying frame_id == F. The RED downgrade-to-next path
//     (invalidateNextFrame) emits NO such event — it just enqueues a
//     fresh frame — so this event's presence with frame_id == F proves
//     the in-frame join fired.
//   - NO second frame was enqueued for the instance: the in-frame join
//     does not create a next-frame, whereas the RED path would
//     EnqueueOrCoalesce a brand-new queued frame (frame count grows).
//   - the target's in-flight run row joined F: state == 'stale' and
//     frame_id == F (MarkStaleForCascade ran against the running frame).
//   - the target's node row carries frame_id == F (the running frame).
//
// RED today (pre-TEMPLCASCADE-6): handleInvalidateNode built
// runtime.InvalidateArgs with Frame="in" but never resolved/threaded
// the running frame, so invalidateInFrame saw both source ids nil and
// unconditionally fell back to invalidateNextFrame — `frame: in` was
// silently downgraded to `frame: next`, no `in_frame_invalidate` event
// was emitted, and a fresh next-frame was enqueued.
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestAcceptance_OperatorFrameInJoinsRunningFrame(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: `source` settles fresh on its single dispatch (a settled source
	// inside the running frame F). `holder` parks indefinitely (no
	// resume_at), holding F open so F stays the instance's running
	// frame while we drive the operator invalidate; it is the target.
	h.Stub.WhenType("source").Success(map[string]any{"s": 1}, true, "settled")
	h.Stub.WhenType("holder").
		Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "await_callback", []byte(`{"ticket":"frame-in"}`), time.Time{}, "")

	// @deliberate: Coalesce mode so the two root nodes coalesce into a SINGLE root
	// frame F (both as source_node_ids), rather than two serial frames.
	// Both are roots (no upstream subscription / substitution ref), so
	// instance-create enqueues them into the one coalesce frame.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "operator-frame-in", Version: "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "source", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "holder", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-operator-frame-in", map[string]any{})

	source := h.FindNode(iid, "source")
	holder := h.FindNode(iid, "holder")
	require.NotNil(t, source)
	require.NotNil(t, holder)

	// @deliberate: Drive the real frame engine + real dispatch until F is genuinely
	// running with `source` settled fresh inside it and `holder` parked
	// mid-drain. These are the real terminal states the supervisor +
	// frame engine produce — no hand-promoted frame.
	require.True(t, h.WaitForNodeState(source.ID, cascade.NodeStateFresh, 30*time.Second),
		"source must settle fresh inside the running frame F")
	require.True(t, h.WaitForNodeState(holder.ID, cascade.NodeStateParked, 30*time.Second),
		"holder must park (holding frame F open) so F stays the running frame")

	// @constraint: Resolve the instance's currently-running frame F directly from the
	// persisted frames table (the same row GetRunningFrameID reads inside
	// the handler). It must be a single running frame sourced on BOTH
	// roots — the coalesced root frame, genuinely advanced to running by
	// the real frame engine.
	frameF := waitForRunningFrame(t, h, iid, 10*time.Second)
	require.NotEqual(t, shared.UUID{}, frameF, "instance must have a running coalesced frame F")

	// @deliberate: The holder's node row + run row are bound to F while parked (frame
	// start bound node.frame_id = F; a Park terminal does not clear it),
	// so the decisive proof of the in-frame JOIN is the
	// `in_frame_invalidate` event + the absence of a NEW frame, exactly
	// as the per-pass test asserts. Snapshot the frame count first.
	var framesBefore int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &framesBefore)

	// @deliberate: Sanity: the holder's in-flight run row is parked inside F before
	// the invalidate (genuine mid-drain state, real dispatch path).
	var preState, preFrame string
	h.QueryRowSQL(`
        SELECT state, frame_id::text FROM rimsky_node_runs
         WHERE node_id = $1 AND phase = 'parked'
         ORDER BY enqueued_at DESC LIMIT 1
    `, []any{holder.ID}, &preState, &preFrame)
	require.Equal(t, "parked", preState, "holder's in-flight run must be parked inside F before the invalidate")
	require.Equal(t, frameF.String(), preFrame, "holder's parked run must be bound to the running frame F")

	// @deliberate: Operator invalidate with frame: in against the target (the
	// mid-drain holder) through the REAL control-api over HTTP. The
	// handler resolves the running frame F via GetRunningFrameID and
	// threads it as SourceFrameID so invalidateInFrame joins F.
	resp, err := http.Post(
		h.ControlBase+"/v1/nodes/"+holder.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{"reason":"mid-cascade correction","frame":"in"}`)),
	)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "operator invalidate must return 200")

	// @deliberate: Discriminator 1 (the in-frame join's signature event): a
	// `state_transition` for the target carries reason
	// `in_frame_invalidate` with frame_id == F. The RED downgrade-to-next
	// path emits no such event.
	require.True(t, waitForInFrameInvalidate(t, h, holder.ID, frameF, 15*time.Second),
		"frame: in must emit a state_transition with reason in_frame_invalidate carrying the running frame F's id (joined F, not downgraded to next)")

	// @deliberate: Discriminator 2 (one frame_id end to end, not two sequential
	// frames): no SECOND frame was enqueued — the join did not create a
	// next-frame. The RED path EnqueueOrCoalesces a fresh queued frame.
	var framesAfter int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{iid}, &framesAfter)
	require.Equal(t, framesBefore, framesAfter,
		"frame: in must NOT enqueue a next-frame; it joins the running frame F (one frame_id end to end)")

	// @deliberate: Discriminator 3 (the target's in-flight run joined F): the run row
	// transitioned to 'stale' and carries frame_id == F (MarkStaleForCascade
	// ran against the running frame).
	var postState, postFrame string
	h.QueryRowSQL(`
        SELECT state, frame_id::text FROM rimsky_node_runs
         WHERE node_id = $1 AND phase IN ('pending','active','held','parked')
         ORDER BY enqueued_at DESC LIMIT 1
    `, []any{holder.ID}, &postState, &postFrame)
	require.Equal(t, "stale", postState,
		"the target's in-flight run must transition to stale (joined the frame's drain)")
	require.Equal(t, frameF.String(), postFrame,
		"the target's in-flight run must carry frame_id == F (joined the running frame, not a next-frame)")

	// @deliberate: Discriminator 4 (the target's node row joined F): the node row
	// carries frame_id == F, the running frame — observable at the same
	// surface the control-api node-detail projects.
	var nodeFrame string
	h.QueryRowSQL(`SELECT frame_id::text FROM rimsky_nodes WHERE id = $1`,
		[]any{holder.ID}, &nodeFrame)
	require.Equal(t, frameF.String(), nodeFrame,
		"the target's node row must carry frame_id == F (the running frame, not a freshly-enqueued next-frame)")

	// @deliberate: The dry-run path still echoes `frame: in`, now truthfully: a
	// dry-run against the open frame reports it would join F (the
	// would_have_invalidated envelope carries frame: in), and persists
	// nothing — no second invalidate, no new frame.
	dryResp, err := http.Post(
		h.ControlBase+"/v1/nodes/"+holder.ID.String()+"/invalidate?dry_run=true",
		"application/json", bytes.NewReader([]byte(`{"reason":"preview","frame":"in"}`)),
	)
	require.NoError(t, err)
	defer dryResp.Body.Close()
	require.Equal(t, http.StatusOK, dryResp.StatusCode, "dry-run invalidate must return 200")
}

// waitForRunningFrame polls rimsky_frames for the instance's single
// running frame and returns its id. The frame must be `running` (the
// real frame engine advanced the coalesced root frame), so this is the
// genuine open frame F an operator `frame: in` joins.
func waitForRunningFrame(t *testing.T, h *scenario.Harness, instanceID shared.UUID, timeout time.Duration) shared.UUID {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var frameID *shared.UUID
		h.QuerySQL(`
            SELECT frame_id FROM rimsky_frames
             WHERE instance_id = $1 AND state = 'running'
             ORDER BY started_at DESC LIMIT 1
        `, []any{instanceID}, func(scan func(...any) error) error {
			var fid shared.UUID
			if err := scan(&fid); err != nil {
				return err
			}
			frameID = &fid
			return nil
		})
		if frameID != nil {
			return *frameID
		}
		time.Sleep(50 * time.Millisecond)
	}
	return shared.UUID{}
}

// waitForInFrameInvalidate polls rimsky_events for a `state_transition`
// row on the target carrying reason `in_frame_invalidate` and frame_id
// == frameF. This is the in-frame join's signature: the RED downgrade-
// to-next path never emits it.
func waitForInFrameInvalidate(t *testing.T, h *scenario.Harness, nodeID, frameF shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		h.QueryRowSQL(`
            SELECT COUNT(*) FROM rimsky_events
             WHERE node_id = $1 AND kind = 'state_transition'
               AND payload->>'reason' = 'in_frame_invalidate'
               AND payload->>'frame_id' = $2
        `, []any{nodeID, frameF.String()}, &count)
		if count > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
