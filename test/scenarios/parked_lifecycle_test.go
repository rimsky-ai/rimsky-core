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

	"github.com/fallguy/rimsky/control/config"
	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
	stubfixture "github.com/fallguy/rimsky/stores/stub/testfixture"
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
	resumeAt := time.Now().Add(2 * time.Second)
	h.Stub.WhenType("worker").
		Park("rate_limit", []byte(`{"hint":"backoff"}`), resumeAt, "session-abc")

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

	// Verify the audit-log entry with the park reason.
	require.True(t, h.WaitForEventKind(worker.ID, "park_requested", 5*time.Second),
		"park_requested audit event should be recorded")

	// Verify the node-run row is in phase='parked' with the
	// resume_at set as we requested.
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

	// Reschedule so the resume can dispatch.
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "resumed")

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
		Park("human_review", []byte(`{"ticket":"R-1"}`), time.Time{}, "")

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
		h.ControlBase+"/admin/instances/"+worker.InstanceID.String()+"/nodes/"+worker.ID.String()+"/invalidate",
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
	h.Stub.WhenType("worker").Park("waiting", nil, time.Time{}, "")

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

// TestParkedLifecycleEmptyReasonPermitted covers E6 case (d). Empty reason
// is permitted (logs WARN supervisor-side); the node still parks.
func TestParkedLifecycleEmptyReasonPermitted(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resumeAt := time.Now().Add(200 * time.Millisecond)
	h.Stub.WhenType("worker").Park("", nil, resumeAt, "")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-empty-reason", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-empty-reason", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateParked, 30*time.Second),
		"worker should reach parked even with empty reason")

	// Confirm the audit-log row was emitted with empty reason permitted.
	require.True(t, h.WaitForEventKind(worker.ID, "park_requested", 5*time.Second),
		"park_requested audit log should be present")
}

// TestParkedLifecycleIntraGraphInvalidateAgainstParked covers E6 case (g).
// Node A parks; node B emits a NamedEvent whose on_event handler invalidates
// A. Verify A wakes via the unified path.
func TestParkedLifecycleIntraGraphInvalidateAgainstParked(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// A parks indefinitely. B emits a named event whose on_event
	// handler invalidates A.
	h.Stub.WhenType("a").Park("await_signal", nil, time.Time{}, "session-A")
	h.Stub.WhenType("b").EmitNamedEvent("ready", []byte(`{"go":true}`)).
		Success(map[string]any{}, true, "b-done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-intra-invalidate", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "b",
				Executor: "stub",
				OnEvent: map[string]node.EventHandler{
					"ready": {
						Invalidate: &node.HandlerInvalidate{Targets: []string{"a"}, Frame: node.FrameNext},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-park-intra", map[string]any{})
	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	// Wait for A to park.
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateParked, 30*time.Second),
		"a should reach parked")
	// Wait for B to complete (emits the event before its terminal).
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b should complete after emitting the event")

	// Reschedule A so the resume terminates normally.
	h.Stub.WhenType("a").Success(map[string]any{}, true, "a-resumed")

	require.True(t, h.WaitForEventKind(a.ID, "parked_resume_started", 10*time.Second),
		"on_event handler should have invalidated A through the unified wake path")
	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a should reach fresh after handler-driven resume")
}

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
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
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
	resumeAt := time.Now().Add(1 * time.Second)
	h.Stub.WhenType("acquirer").
		Park("checkpoint", []byte(`{"step":1}`), resumeAt, "tok-1")
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
					Type:         "inheritor",
					Executor:     "stub",
					Dependencies: []string{"acquirer"},
				},
				scenario.WithInherits(scenario.Inherit("held")),
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
	require.Equal(t, "checkpoint", *parkedReason)

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

	// Reschedule the acquirer script so the resume can complete.
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "resumed")

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
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1`, uuid.UUID(iid),
		).Scan(&lhCount))
		if lhCount == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Equal(t, 0, lhCount,
		"claim_handle row must be deleted after auto-terminal Commit")
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
		Capabilities: locks.Capabilities{WriteSemanticsAllowed: []locks.WriteSemantics{locks.WriteSemanticsSync}},
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
	h.Stub.WhenType("acquirer").Park("waiting_held", nil, time.Time{}, "")
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
					Type:         "inheritor",
					Executor:     "stub",
					Dependencies: []string{"acquirer"},
				},
				scenario.WithInherits(scenario.Inherit("held")),
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
	require.True(t, h.WaitForEventKind(acq.ID, "park_timeout", 15*time.Second),
		"watchdog should fire park_timeout")
	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateFailed, 15*time.Second),
		"acquirer should land in failed after park_timeout")
	require.True(t, h.WaitForWorkerRequestDeleted(acq.ID, 15*time.Second),
		"node-run should be deleted after timeout abandon")

	// Auto-terminal Abandon: the rimsky_claim_handles row is removed
	// AND the producer's Abandon verb fired (visible on store.Calls()).
	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1`, uuid.UUID(iid),
	).Scan(&lhCount))
	require.Equal(t, 0, lhCount,
		"claim_handle row must be deleted after auto-terminal Abandon")

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
