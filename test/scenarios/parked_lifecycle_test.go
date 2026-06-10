// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Parked-state lifecycle scenario tests covering the runtime built in
// E1–E5: applyTerminalPark + SweepParkedNodes + the unified-invalidate
// wake path + max_park_duration overrun. Per the 2026-05-08 platform-
// extensions plan E6.

package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestParkedLifecycleResumeOnDeadline covers E6 case (a). Executor emits
// Park with resume_at 2s in the future. SweepParkedNodes wakes
// the row when the deadline elapses.
//
// Note: the sweep transitions phase parked→pending and node state
// parked→stale; the standard scheduler/ready-sweep + frame engine then
// re-dispatches the row. The test asserts the wake event was logged;
// the post-wake completion is asserted by the external-invalidate test
// (the wake mechanism is identical).
func TestParkedLifecycleResumeOnDeadline(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// resumeAt is the wall-clock deadline at which SweepParkedNodes wakes
	// the parked node. It must outlast deploy → create → reach-parked plus
	// the two parked-window probes (phase/resume_at and the leaf-run
	// lineage), since those read the row while it is still parked. The race
	// that flaked this test under full-suite load was an *accumulated*-
	// latency one: the phase probe used to run dead last — after the
	// park-signal wait and the lineage probe — so their combined latency
	// could push it past the deadline, the sweep having already woken the
	// node (phase then observed `completed`). The fix below reorders the
	// two deadline-sensitive probes to run immediately after the parked
	// transition; 15s then leaves clear buffer even on a heavily loaded
	// host while keeping the resume comfortably inside the post-resume 30s
	// `WaitForNodeState` windows.
	resumeAt := time.Now().Add(15 * time.Second)
	h.Stub.WhenType("worker").
		Park(genv1.ParkReason_PARK_REASON_SNOOZE, "rate_limit", []byte(`{"hint":"backoff"}`), resumeAt, "session-abc")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-deadline", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-deadline", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	// Wait for the park transition.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateParked, 30*time.Second),
		"worker should reach parked")

	// Two things must happen immediately after the parked transition,
	// before any further wait can let the resume_at deadline elapse:
	//  (a) swap the worker to its resume Success script, so the deadline
	//      wake dispatches cleanly instead of re-running the Park script
	//      (`WhenType` replaces the entire per-type script in the stub);
	//  (b) capture the parked phase + persisted resume_at while the row is
	//      still parked.
	// Running both here — rather than dead last, after the park-signal and
	// lineage probes — removes the accumulated-latency race that flaked
	// this test under full-suite load: the phase read now runs ~ms after
	// the parked transition instead of seconds later.
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "resumed")

	var phase string
	var resumeAtStored *time.Time
	h.QueryRowSQL(
		`SELECT phase, resume_at FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{worker.ID},
		&phase, &resumeAtStored,
	)
	require.Equal(t, "parked", phase, "node-run should be in parked phase")
	require.NotNil(t, resumeAtStored, "resume_at should be persisted")
	t.Logf("parked row: phase=%s resume_at=%v (now=%v, resume_at-now=%v)",
		phase, *resumeAtStored, time.Now(), time.Until(*resumeAtStored))

	// Verify the canonical terminal/park/* signal event was emitted
	// (Pass 5 retired the legacy `park_requested` fixed-string row in
	// favor of the signal type-path). The test uses the snooze flavor
	// per its `resumeAt` deadline; the executor stub maps a Park with
	// `resume_at` to ParkReason_SNOOZE. WaitForEventKind matches the
	// historical event, so it is race-free against the resume.
	require.True(t, h.WaitForEventKind(worker.ID, "terminal/park/snooze", 5*time.Second),
		"terminal/park/snooze signal event should be recorded")

	// Lineage assertion: the leaf-run lineage row for the parked terminal
	// MUST carry settling_signal_type=terminal/park/snooze. EmitLeafRunLineage
	// in `runner_terminal_park.go::applyTerminalPark` threads `parkSigType`
	// through; an empty field would mean the writer dropped the value. This
	// query is LIMIT 1 ORDER BY observed_at DESC, so it must still run inside
	// the parked window (the 15s budget covers it) — once the resume lands a
	// newer leaf-run row, it would return that instead.
	var parkSettlingSignal string
	h.QueryRowSQL(
		`SELECT record->>'settling_signal_type' FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run' AND record->>'node_id' = $1
		 ORDER BY observed_at DESC LIMIT 1`,
		[]any{worker.ID.String()},
		&parkSettlingSignal,
	)
	require.Equal(t, "terminal/park/snooze", parkSettlingSignal,
		"park leaf-run lineage row should carry settling_signal_type=terminal/park/snooze")

	require.True(t, h.WaitForEventKind(worker.ID, "parked_resume_started", 30*time.Second),
		"sweep should wake the parked node when resume_at elapses")
	// Verify the persisted resume_reason is "deadline_elapsed" — the
	// runner reads this from rimsky_node_runs.wake_reason and
	// attaches it to the ExecuteRequest.resume_context so executors can
	// distinguish deadline-elapsed wakes from external invalidates.
	row := lastEventPayload(t, h, worker.ID, "parked_resume_started")
	require.Equal(t, "deadline_elapsed", row["resume_reason"],
		"deadline-elapsed wake must persist resume_reason=deadline_elapsed; "+
			"got %v", row["resume_reason"])
	// And the worker should ultimately reach fresh after the resume
	// dispatch completes.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh after deadline-elapsed resume")
}

// TestParkedLifecycleResumeOnExternalInvalidate covers E6 case (b). Park
// indefinitely (no resume_at), then admin POSTs invalidate to wake.
func TestParkedLifecycleResumeOnExternalInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Indefinite park — no resume_at.
	h.Stub.WhenType("worker").
		Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "human_review", []byte(`{"ticket":"R-1"}`), time.Time{}, "")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-external", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-external", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateParked, 30*time.Second),
		"worker should reach parked (indefinite)")

	// Reschedule the script so the resume completes.
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "after-review")

	// External admin invalidate.
	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(
		h.ControlBase+"/v1/admin/instances/"+worker.InstanceID.String()+"/nodes/"+worker.ID.String()+"/invalidate",
		"application/json", bytes.NewReader(body),
	)
	require.NoError(t, err)
	resp.Body.Close()

	require.True(t, h.WaitForEventKind(worker.ID, "parked_resume_started", 10*time.Second),
		"admin invalidate should wake the parked node")
	// Verify resume_reason payload uses external_invalidate.
	row := lastEventPayload(t, h, worker.ID, "parked_resume_started")
	require.Equal(t, "external_invalidate", row["resume_reason"])

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker should reach fresh after external resume")
}

// TestParkedLifecycleMaxParkDurationOverrun covers E6 case (c). Set
// max_park_duration on the template; park indefinitely (no resume_at);
// after the duration, the watchdog forces failure with
// error_class=park_timeout.
func TestParkedLifecycleMaxParkDurationOverrun(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// Park indefinitely so SweepParkedNodes' watchdog branch fires; the
	// runtime measures overrun against parked_at + max_park_duration.
	h.Stub.WhenType("worker").Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "waiting", nil, time.Time{}, "")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-overrun", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:            "worker",
				Executor:        "stub",
				MaxParkDuration: "1s",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-overrun", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateParked, 30*time.Second),
		"worker should reach parked")

	// Wait past the cap. SweepParkedNodes runs every tick (250ms), so
	// within 5s the watchdog should force failure.
	require.True(t, h.WaitForEventKind(worker.ID, "park_timeout", 15*time.Second),
		"watchdog should fire park_timeout after max_park_duration")
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 15*time.Second),
		"worker should land in failed after park_timeout")
}

// TestParkedLifecycleUnspecifiedReasonRejected retired per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md:
// PARK_REASON_UNSPECIFIED was removed entirely in the 7→2 collapse —
// the only legal ParkReason values are now AWAIT_CALLBACK and SNOOZE,
// and proto3 dropped the unspecified zero value at the wire layer.
// The "reject unspecified" runtime test is no longer expressible.

// TestParkedLifecycleIntraGraphInvalidateAgainstParked (E6 case (g))
// retired per spec
// .ok-planner/specs/2026-06-03-instance-lifecycle-durable-by-default-design.md.
//
// The case exercised the intra-graph cascade wake of a parked receiver
// (wakeParkedReceiverInTx, resume_reason=cascade_wake) end to end by
// re-invalidating an already-settled upstream B so the new frame's
// settlement cascade would wake parked A. That only ever passed because
// of the frame-end defect this spec fixes: pre-fix, a parked node_run did
// NOT hold its frame open, so A's initial frame drained to `completed`
// while A sat parked, freeing the serial queue to start the re-
// invalidate's new frame. With the fix (a parked node_run holds its frame
// open), A's frame stays running, so under serial-queue resolution the
// re-invalidate's new frame cannot start ahead of it — and there is no
// supported user operation that wakes a parked node by re-invalidating an
// upstream: a user invalidate only ever creates (or coalesces into) a
// frame, never re-fires a node inside an already-open frame. The
// supported parked-wake paths remain covered — deadline (case (a)),
// external admin invalidate (case (b)), and the durable-by-default gate
// in parked_holds_frame_e2e_test.go — and the wakeParkedReceiverInTx
// primitive stays covered by the runtime unit test
// runtime/hard_dep_cascade_test.go::TestPullHardDepUpstreams_WakesParkedUpstream.

// TestParkedLifecycleHeldClaimRetentionAcrossPark covers E6 case (e).
// A node holds a claim, parks, then resumes. The claim handle row in
// rimsky_claim_handles survives across the park boundary (its parent
// node-run's parked phase does not delete the handle), and the
// resume runs the same handle through to the active terminal which
// fires the auto-terminal Commit/Abandon.
//
// Without held-claim retention across park, the auto-terminal Abandon
// would either fire prematurely at park (collapsing the held subgraph)
// or never fire (leaking producer state). The test asserts the handle
// row is present at all three checkpoints: pre-park, mid-park, and
// post-resume completion (post-terminal it is auto-deleted).
//
// Notes (diagnostic — testcontainer-startup-bound, not a
// production-code bug):
//
//	Symptom (flagged across cycles 4, 6, 7): under heavy parallel
//	load the resume_at scheduling could fire BEFORE the Success
//	script replaced the Park script, causing a re-park loop and a
//	WaitForNodeState(..., Fresh) timeout.
//
//	Root cause located: NOT a race in
//	runtime.SweepParkedNodes or the wake-parked-node path. The
//	race is between (a) the test's own setup sequence (deploy
//	template → create instance → wait-for-parked → SQL probes →
//	re-script stub) and (b) the wall-clock resume_at deadline. The
//	setup sequence's wall-time is dominated by testcontainer
//	cold-start: each scenario test calls pgmigrate.OpenDriver which
//	spins up its own postgres:14-alpine container; the harness's
//	per-poll Docker state-query is "~1-6s under saturated parallel
//	load; occasional 15-20s spikes" (see
//	testpg/testpg.go::StartFreshPostgresDSN). Under the
//	historical 1-2s resume_at budget the sweep could fire before
//	the rescript landed.
//
//	Ruled out: SweepParkedNodes' wake path (sub-second once
//	triggered), the auto-terminal Commit logic (separate held-
//	subgraph completion test exercises it directly), the stub
//	executor's WhenType swap (in-process, instantaneous), the
//	wait-set drain logic.
//
//	Resolution: the 10s resume_at + the 30s WaitForNodeState
//	budgets below were chosen to cover one testcontainer
//	cold-start spike plus the in-process steady-state latency,
//	with no overlap into the resume window. Do NOT compress these
//	without first re-instrumenting the harness to share a single
//	postgres container across scenarios (see also
//	`runtime/sweep_claim_handle_retention_test.go`
//	::TestSweepClaimHandleRetention_SweepsSubgraphCommittedPastCutoff
//	for the same testcontainer-cold-start diagnosis on a
//	non-scenario test).
func TestParkedLifecycleHeldClaimRetentionAcrossPark(t *testing.T) {
	t.Parallel()
	// Two-node scenario: `acquirer` holds a scope-claim with alias
	// "held"; the held subgraph contains both itself and the
	// downstream `inheritor`. The acquirer parks while the inheritor
	// is still pending, exercising rimsky_claim_handles retention
	// across the active → parked transition. After resume +
	// completion of both nodes, auto-terminal fires Commit on the
	// held claim and the claim-handle row is deleted.
	//
	// Uses a scope-claim (not a pick-policy queue) so the resume
	// dispatch's fresh acquisition tx doesn't fight an exhausted
	// queue.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	// resumeAt must be far enough in the future that, under heavy
	// parallel testcontainer load, the entire setup-through-parked-
	// state-probe sequence completes BEFORE SweepParkedNodes can pick
	// the row up — otherwise the sweep dispatches under the still-Park
	// script (the resume's Success script is registered below, after
	// parked-state probes), the node re-parks, and the test times out
	// on `WaitForNodeState(..., Fresh)`. Observed parallel setup
	// latency on a loaded host runs ~5-10s; 10s gives clear buffer
	// while keeping resume comfortably inside the post-resume 30s
	// WaitForNodeState windows. The original 1s budget assumed cold-
	// container speeds and was the documented flake source.
	resumeAt := time.Now().Add(10 * time.Second)
	h.Stub.WhenType("acquirer").
		Park(genv1.ParkReason_PARK_REASON_SNOOZE, "checkpoint", []byte(`{"step":1}`), resumeAt, "tok-1")
	// Inheritor is pre-scripted but won't be reached until the
	// acquirer resumes and completes.
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "inheritor-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-held-retention", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "/held-A", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-held", map[string]any{})
	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateParked, 30*time.Second),
		"acquirer should reach parked")

	// Re-script the acquirer for the resume dispatch BEFORE the parked-
	// state SQL probes run, and BEFORE the wall-clock approaches the
	// scripted resume_at. WhenType replaces the entire script in the
	// stub's per-type map, so this swap turns the next Execute call on
	// "acquirer" into a Success terminal. Pairing the swap with the
	// generous resume_at above closes the time-based wake race: even
	// under heavy testcontainer load the Success script is in place
	// long before SweepParkedNodes can pick the parked row up.
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "resumed")

	// While parked, verify the node-run row is in phase='parked'
	// AND the rimsky_claim_handles row for the held claim survives
	// (the auto-terminal Abandon must not fire while the inheritor
	// hasn't run yet).
	var phase string
	var parkedReason *string
	h.QueryRowSQL(
		`SELECT phase, parked_reason FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{acq.ID},
		&phase, &parkedReason,
	)
	require.Equal(t, "parked", phase, "node-run must be in parked phase")
	require.NotNil(t, parkedReason, "parked_reason must survive parked transition")
	require.Equal(t, "snooze", *parkedReason,
		"parked_reason should store the enum form (snake_case); TIME_WAIT collapsed to SNOOZE per the 2026-05-22 ParkReason collapse")

	// Held claim_handle row exists during park: the held subgraph
	// (acquirer + inheritor) is still active, so auto-terminal cannot
	// fire yet.
	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.is_held = TRUE`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 1, lhCount,
		"held claim_handle row must survive across the active → parked transition")

	require.True(t, h.WaitForEventKind(acq.ID, "parked_resume_started", 30*time.Second),
		"sweep should wake the parked acquirer")
	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should reach fresh after resume")
	require.True(t, h.WaitForNodeState(inh.ID, cascade.NodeStateFresh, 30*time.Second),
		"inheritor should reach fresh after acquirer commits")

	require.True(t, h.WaitForWorkerRequestDeleted(acq.ID, 30*time.Second),
		"acquirer node-run should be deleted after resume completes")
	require.True(t, h.WaitForWorkerRequestDeleted(inh.ID, 30*time.Second),
		"inheritor node-run should be deleted after completion")

	// Auto-terminal fires Commit (both held subgraph members completed
	// successfully); claim_handle rows are then removed. Allow a
	// generous polling window — the auto-terminal sweep runs at the
	// scheduler tick cadence, and the resume path may briefly hold a
	// transient second claim_handle until its own terminal release
	// fires.
	// Post-Stage-3 of the claim-handle state-column refactor: terminal
	// flips state (Promote-not-delete). Assert the row reaches state=
	// committed (auto-terminal Commit) instead of being deleted.
	var activeCount int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1 AND lh.state = 'active'`, uuid.UUID(iid),
		).Scan(&activeCount))
		if activeCount == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Equal(t, 0, activeCount,
		"no active claim_handle rows must remain after auto-terminal Commit")
	var committedCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'committed'`, uuid.UUID(iid),
	).Scan(&committedCount))
	require.Greater(t, committedCount, 0,
		"at least one claim_handle row must be state=committed after auto-terminal Commit")
}

// TestParkedLifecycleParkTimeoutAbandonsHeldClaim covers E6 case (c)'s
// held-claim path. A held node parks indefinitely and overruns
// max_park_duration; the watchdog must fail the row AND fire Abandon
// on the held claim handle (blessed invariant 13). Without the
// abandonHeldClaimsForOverdueNode path, the rimsky_claim_handles row
// would survive and only be reaped by the orphan-claim sweep — without
// firing the Abandon verb that the producer requires for cleanup.
func TestParkedLifecycleParkTimeoutAbandonsHeldClaim(t *testing.T) {
	t.Parallel()
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "waiting_held", nil, time.Time{}, "")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-timeout-held", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:            "acquirer",
					Executor:        "stub",
					MaxParkDuration: "1s",
				},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "/held-T", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-timeout-held", map[string]any{})
	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)

	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateParked, 30*time.Second),
		"acquirer should reach parked")

	// The watchdog branch in SweepParkedNodes runs failOverdueParkedRow,
	// which marks the held claim-holder rows 'failed' and fires
	// CheckAndFireResolution. With any failed holder, auto-terminal
	// resolves the claim by firing Abandon on the producer (blessed
	// invariant 13).
	//
	// The 30s wait budget (extended from cycle-4's 15s) absorbs the
	// scheduler-tick + sweep-tick interleave plus testcontainers/Docker
	// latency under heavy parallel load.
	require.True(t, h.WaitForEventKind(acq.ID, "park_timeout", 30*time.Second),
		"watchdog should fire park_timeout")
	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateFailed, 30*time.Second),
		"acquirer should land in failed after park_timeout")
	require.True(t, h.WaitForWorkerRequestDeleted(acq.ID, 30*time.Second),
		"node-run should be deleted after timeout abandon")

	// Auto-terminal Abandon: post-Stage-3 of the claim-handle state-
	// column refactor, the rimsky_claim_handles row is PROMOTED (not
	// deleted); the producer's Abandon verb fired (visible on
	// store.Calls()). Assert the row is in state=abandoned.
	var abandonedCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'abandoned'`, uuid.UUID(iid),
	).Scan(&abandonedCount))
	require.Greater(t, abandonedCount, 0,
		"at least one claim_handle row must be state=abandoned after auto-terminal Abandon")
	var activeCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.state = 'active'`, uuid.UUID(iid),
	).Scan(&activeCount))
	require.Equal(t, 0, activeCount,
		"no active claim_handle rows must remain after auto-terminal Abandon")

	abandonSeen := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range store.Calls() {
			if c.Verb == "abandon" {
				abandonSeen = true
				break
			}
		}
		if abandonSeen {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, abandonSeen,
		"producer Abandon verb must fire on park-timeout for held claim (blessed invariant 13)")
}

// lastEventPayload returns the payload JSON for the most recent
// rimsky_events row of (node, kind). Used to assert resume_reason etc.
func lastEventPayload(t *testing.T, h *scenario.Harness, nodeID shared.UUID, kind string) map[string]any {
	t.Helper()
	var rawJSON []byte
	h.QueryRowSQL(
		`SELECT payload::text FROM rimsky_events WHERE node_id = $1 AND kind = $2 ORDER BY occurred_at DESC LIMIT 1`,
		[]any{nodeID, kind},
		&rawJSON,
	)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rawJSON, &out))
	return out
}

// _ uses persistence to keep the import alive when the scenario test
// adds future raw queries needing persistence types.
var _ = persistence.NodeRow{}
