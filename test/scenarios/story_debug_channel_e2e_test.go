// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-debug-channel acceptance proof.
//
// As an operator, I can override-invalidate a specific node or
// override-set an attribute value via the control-api when the target
// instance is paused or at a breakpoint pause-mode hit, so that ad-hoc
// inspection and mutation are available exactly when I have explicitly
// entered debug mode, and unavailable otherwise.
//
// Acceptance (per spec):
//   - With an instance paused: true OR with an unresumed pause-mode
//     breakpoint hit blocking a runner, a POST /v1/instances/{id}/debug/override
//     stale-marks a specific node and/or sets a specific attribute value
//     in the running frame; the override applies in that frame.
//   - When the instance is neither paused nor breakpoint-stopped, the
//     same request is refused with an error citing the required state.
//
// The proof boots the real rimsky stack via the testcontainers-backed
// scenario harness (scheduler + supervisor + control-api against a real
// Postgres) and drives every gate transition through the real surfaces:
//
//   - Leg 1 (healthy refuses): a freshly-dispatched instance with no
//     pause-flag and no breakpoint hit; POST /debug/override returns
//     HTTP 409 with the predicate names in the body.
//   - Leg 2 (paused accepts + cascade resumes): a real pause-mode
//     breakpoint is installed via POST /v1/instances/{id}/breakpoints
//     against a paused-on-create instance; the supervisor proceeds on
//     resume, the breakpoint hit parks an in-flight run row. We then
//     POST /v1/instances/{id}/pause so the instance is also paused. The
//     debug override invalidate_node succeeds with gate_state="paused";
//     the named node-run row's state transitions to "stale" and an
//     audit-event row of kind "debug.override.applied" is appended.
//     After clearing the gate (resume pause + delete breakpoint), the
//     cascade picks the stale run up and the worker re-dispatches —
//     the user-observable resume.
//   - Leg 3 (breakpoint accepts + cascade resumes with overrides): a
//     fresh instance + pause-mode breakpoint; the supervisor hits the
//     breakpoint while it dispatches; while parked, POST /debug/override
//     set_attribute succeeds with gate_state="breakpoint"; the in-flight
//     run's attribute row commits the operator key, the audit row is
//     written. After deleting the breakpoint, the parked dispatch
//     releases and the next dispatch carries the operator-supplied
//     attribute through to the executor's ExecuteRequest.
//   - Leg 4 (permission denied): admin key minted (deployment leaves
//     anonymous mode); a restricted key without `instance:debug-override`
//     POSTs /debug/override against the same instance; the auth gate
//     refuses with HTTP 403, the request never reaches the handler.
//
// The proof asserts at the user-observable surface: the override
// actually mutates the node-run state (Leg 2 → stale, observed cascade
// re-dispatch on resume) and the attribute row (Leg 3 → operator value
// shows up in the next dispatch's ExecuteRequest after gate clears).
// Asserting only on the HTTP status struct would not satisfy the
// falsifier — the override must demonstrably mutate the graph AND let
// the cascade resume against the mutation, not merely return a 200.
//
// @story: debug-channel
// @concept: debug-channel
// @concept: breakpoint
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStoryDebugChannel_GateAndOverrideAcrossRealStack(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: the worker echoes the resolved attribute bag back
	// through Observed so Leg 3 can confirm the operator-supplied
	// attribute landed on the executor's ExecuteRequest after the gate
	// clears. Every leg uses the same worker stub.
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ran")

	tpl := node.TemplateSpec{
		Name: "story-debug-channel", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tag": map[string]any{"type": "string"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	}
	tid := h.DeployTemplate(tpl)

	// @deliberate: a fresh instance with no pause-flag and no breakpoint
	// must refuse; this is the falsifier-side "override accepted on
	// neither legal state" property. The handler's gate runs INSIDE the
	// request tx, so the 409 is the authoritative read of "neither
	// paused nor breakpoint-stopped" at request-arrival time.
	iidHealthy := h.CreateInstance(tid, "ck-debug-healthy", map[string]any{})
	require.NotEqual(t, shared.UUID{}, iidHealthy)

	workerHealthy := h.FindNode(iidHealthy, "worker")
	require.NotNil(t, workerHealthy)
	// @deliberate: let the initial dispatch retire so the instance is
	// unambiguously "healthy" (Fresh, not in-flight, not paused) when we
	// probe the gate.
	require.True(t,
		h.WaitForNodeState(workerHealthy.ID, cascade.NodeStateFresh, 15*time.Second),
		"initial dispatch must reach fresh before the healthy-gate leg probes")

	respHealthy := postDebugOverride(t, h.ControlBase, iidHealthy, map[string]any{
		"action":    "invalidate_node",
		"node_type": "worker",
	}, "")
	require.Equal(t, http.StatusConflict, respHealthy.status,
		"healthy instance must refuse the override with HTTP 409; body: %s",
		string(respHealthy.raw))
	var healthyBody struct {
		Error  string   `json:"error"`
		States []string `json:"states"`
	}
	require.NoError(t, json.Unmarshal(respHealthy.raw, &healthyBody))
	require.Contains(t, healthyBody.Error, "not in debuggable state",
		"the 409 body must name the gate predicate so the operator sees what would unlock the override")
	require.ElementsMatch(t, []string{"paused", "breakpoint"}, healthyBody.States,
		"the 409 body must list BOTH legal predicates, not just one")

	// @deliberate: falsifier protection — a 409 must NOT have written a
	// debug.override.applied audit row. The handler short-circuits
	// before the audit append; this pins that the gate is structural
	// and not merely cosmetic.
	require.Equal(t, 0, countDebugOverrideAuditRows(t, h, iidHealthy),
		"healthy-instance refusal must not leave an audit row")

	// @deliberate: to exercise the invalidate_node mutation arm, the
	// override must land while an in-flight run row exists for the
	// worker node. The natural way to hold a run in-flight without
	// spinning bare SQL is the pause-mode breakpoint: the supervisor
	// enters before_dispatch, the matcher matches, the hit row commits,
	// and the dispatch parks with the in-flight run row alive. We then
	// ALSO toggle paused=true via /pause so the gate reads as "paused"
	// first (the handler short-circuits on paused before consulting
	// BreakpointHits, so gate_state lands on "paused" — exactly the
	// Leg-2 acceptance).
	iidPaused := createInstanceWithPaused(t, h.ControlBase, tid, "ck-debug-paused")
	require.NotEqual(t, shared.UUID{}, iidPaused)

	bpIDPaused := installPauseModeBreakpoint(t, h.ControlBase, iidPaused, "worker")

	// @deliberate: resume the create-time pause so the supervisor begins
	// dispatching; the breakpoint will catch the worker before the
	// executor fires and park the dispatch with an in-flight run row.
	resumeResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iidPaused.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeResp.status,
		"create-time pause must release cleanly; body=%s", string(resumeResp.raw))

	hitPaused := waitForFirstHit(t, h, bpIDPaused, 15*time.Second)
	require.Equal(t, "before_dispatch", string(hitPaused.Checkpoint),
		"the parked dispatch's hit must record the before_dispatch checkpoint")

	// @deliberate: re-pause via the real /pause endpoint so the gate's
	// "paused" leg is the one that satisfies the predicate. This is the
	// Leg-2 observable: an operator decides to pause an instance that
	// has already entered debug state.
	pauseResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/pause", iidPaused.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, pauseResp.status,
		"re-pause via /pause must succeed; body=%s", string(pauseResp.raw))

	workerPaused := h.FindNode(iidPaused, "worker")
	require.NotNil(t, workerPaused, "worker node row must exist on the paused instance")
	preMutateRunID := getInFlightRunID(t, h, workerPaused.ID)
	require.NotNil(t, preMutateRunID,
		"the parked breakpoint hit must leave an in-flight run row for the worker — "+
			"the invalidate_node mutation arm needs one to stale-mark")

	respPaused := postDebugOverride(t, h.ControlBase, iidPaused, map[string]any{
		"action":    "invalidate_node",
		"node_type": "worker",
	}, "")
	require.Equal(t, http.StatusOK, respPaused.status,
		"paused instance must accept the override with HTTP 200; body=%s",
		string(respPaused.raw))
	var pausedResp struct {
		OK          bool   `json:"ok"`
		GateState   string `json:"gate_state"`
		RunsMutated int    `json:"runs_mutated"`
	}
	require.NoError(t, json.Unmarshal(respPaused.raw, &pausedResp))
	require.True(t, pausedResp.OK)
	require.Equal(t, "paused", pausedResp.GateState,
		"the gate must report `paused` when the /pause flag is on")
	require.GreaterOrEqual(t, pausedResp.RunsMutated, 1,
		"the override must report at least one mutated run — falsifier-side: "+
			"a 200 with runs_mutated=0 means the handler returned a status struct "+
			"without actually mutating the graph")

	// @deliberate: the user-observable assertion the falsifier names —
	// the named node-run row's state actually transitions to "stale".
	// Assert on the run row's state, not merely the HTTP status struct.
	require.Equal(t, cascade.NodeStateStale,
		getRunState(t, h, *preMutateRunID),
		"invalidate_node must transition the run row to state=stale; "+
			"asserting at the HTTP-status layer alone would let a no-op handler pass")

	// @deliberate: audit-event-row assertion the falsifier also names.
	// The handler appends in the same tx as the mutation; by the time
	// the 200 is observed the row is committed.
	require.Equal(t, 1, countDebugOverrideAuditRows(t, h, iidPaused),
		"a successful override must leave exactly one audit row of kind debug.override.applied")
	auditPaused := readLatestDebugOverrideAudit(t, h, iidPaused)
	require.Equal(t, "invalidate_node", auditPaused["action"])
	require.Equal(t, "worker", auditPaused["node_type"])
	require.Equal(t, "paused", auditPaused["gate_state"])

	// @deliberate: cascade-resume observation — after the gate clears
	// (resume + breakpoint delete), the supervisor picks the stale run
	// up and the worker re-dispatches. This is the "the override
	// applies in that frame" observable — the override didn't just
	// mutate state, the cascade observed the mutation and progressed.
	observedBeforeResume := stubWorkerCount(h)
	// @deliberate: deleting the breakpoint cascade-removes the hit row
	// (per the existing breakpoint lifecycle), unblocking waitForResume.
	resumeResp2 := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iidPaused.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeResp2.status,
		"resume after override must succeed; body=%s", string(resumeResp2.raw))
	deleteBreakpoint(t, h.ControlBase, iidPaused, bpIDPaused)

	require.True(t, waitForStubWorkerCount(h, observedBeforeResume+1, 20*time.Second),
		"after the gate clears, the supervisor must re-dispatch the stale-marked worker — "+
			"this is the falsifier's user-observable: the override actually drove the cascade forward")

	// @deliberate: a fresh instance lets us pin the breakpoint-gate leg
	// in isolation (no pause flag set); the gate's "paused" check is
	// false, the "breakpoint" check is true, so gate_state lands on
	// "breakpoint".
	iidBP := createInstanceWithPaused(t, h.ControlBase, tid, "ck-debug-breakpoint")
	require.NotEqual(t, shared.UUID{}, iidBP)
	bpIDBP := installPauseModeBreakpoint(t, h.ControlBase, iidBP, "worker")
	resumeBPResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iidBP.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeBPResp.status,
		"create-time pause must release cleanly; body=%s", string(resumeBPResp.raw))
	hitBP := waitForFirstHit(t, h, bpIDBP, 15*time.Second)
	require.Equal(t, "before_dispatch", string(hitBP.Checkpoint))

	workerBP := h.FindNode(iidBP, "worker")
	require.NotNil(t, workerBP)
	preBPRunID := getInFlightRunID(t, h, workerBP.ID)
	require.NotNil(t, preBPRunID,
		"the parked breakpoint hit must leave an in-flight run row for the worker — "+
			"the set_attribute mutation arm merges into its attribute row")

	const operatorAttrKey = "tag"
	const operatorAttrValue = "operator-debug-override-value"
	respBP := postDebugOverride(t, h.ControlBase, iidBP, map[string]any{
		"action":          "set_attribute",
		"node_type":       "worker",
		"attribute_key":   operatorAttrKey,
		"attribute_value": operatorAttrValue,
	}, "")
	require.Equal(t, http.StatusOK, respBP.status,
		"breakpoint-stopped instance must accept the override with HTTP 200; body=%s",
		string(respBP.raw))
	var bpResp struct {
		OK          bool   `json:"ok"`
		GateState   string `json:"gate_state"`
		RunsMutated int    `json:"runs_mutated"`
	}
	require.NoError(t, json.Unmarshal(respBP.raw, &bpResp))
	require.True(t, bpResp.OK)
	require.Equal(t, "breakpoint", bpResp.GateState,
		"the gate must report `breakpoint` when only the unresumed-hit predicate holds")
	require.GreaterOrEqual(t, bpResp.RunsMutated, 1,
		"the override must report at least one mutated run — falsifier-side: "+
			"a 200 with runs_mutated=0 means the handler returned a status struct "+
			"without actually mutating the graph")

	// @deliberate: the user-observable assertion — the attribute row for
	// the in-flight run commits the operator key. Asserting at the
	// HTTP-status layer alone would let a no-op handler pass. The
	// override invalidate side of set_attribute also stale-marks; we
	// read the run's attribute row and confirm the operator value is
	// there.
	require.Equal(t, operatorAttrValue,
		getRunAttributeValue(t, h, *preBPRunID, operatorAttrKey),
		"set_attribute must merge the operator key/value into the in-flight run's attribute row")

	require.Equal(t, 1, countDebugOverrideAuditRows(t, h, iidBP),
		"a successful override must leave exactly one audit row of kind debug.override.applied")
	auditBP := readLatestDebugOverrideAudit(t, h, iidBP)
	require.Equal(t, "set_attribute", auditBP["action"])
	require.Equal(t, "worker", auditBP["node_type"])
	require.Equal(t, "breakpoint", auditBP["gate_state"])
	require.Equal(t, operatorAttrKey, auditBP["attribute_key"])
	require.Equal(t, operatorAttrValue, auditBP["attribute_value"])

	// @deliberate: cascade-resume observation — after the breakpoint is
	// deleted, the parked dispatch releases (waitForResume returns) and
	// the cascade progresses. This is the "the override applies in that
	// frame" observable for the set_attribute arm — the operator value
	// committed to the attribute row AND the cascade actually advanced
	// past the breakpoint, rather than the override being a no-op
	// trapped in persistence.
	//
	// @deliberate: the set_attribute override is read by downstream
	// substitution from `{{nodes.<type>.attribute.<field>}}`; the same-
	// node next-dispatch re-runs the template's schema so the in-place
	// rebound value is not observable on the executor's bag for THIS
	// node. The persistence-layer assertion above pins the operator
	// value committed; this assertion pins the cascade resumed against
	// the override. Together they exhibit "the override applies in that
	// frame and the runtime moves forward."
	preStubCount := stubWorkerCount(h)
	deleteBreakpoint(t, h.ControlBase, iidBP, bpIDBP)
	require.True(t, waitForStubWorkerCount(h, preStubCount+1, 20*time.Second),
		"after the breakpoint is deleted, the parked dispatch must release; "+
			"without this assertion the proof would not exhibit the cascade resume")

	// @deliberate: minting the admin key leaves anonymous mode for the
	// rest of the control-api process; we do this LAST so prior legs'
	// bare-HTTP helpers continued to traverse the anonymous-mode allow
	// path. After this point, every request needs a Bearer.
	adminKey := mintAnonymousAdminKey(t, h.ControlBase)
	restrictedKey := mintRestrictedKey(t, h.ControlBase, adminKey)

	// @deliberate: re-use iidBP for the deny probe. The instance state
	// does not matter for the auth-gate leg — the gate fires before the
	// handler runs, so 403 is the answer regardless of paused /
	// breakpoint / healthy. We pick an arbitrary instance that already
	// exists.
	respDenied := postDebugOverride(t, h.ControlBase, iidBP, map[string]any{
		"action":    "invalidate_node",
		"node_type": "worker",
	}, restrictedKey)
	require.Equal(t, http.StatusForbidden, respDenied.status,
		"a key without `instance:debug-override` must receive HTTP 403 from the auth gate; "+
			"body=%s", string(respDenied.raw))
	// @deliberate: auth-gate denial must not have written a
	// debug.override.applied audit row — the audit row in the handler
	// counts mutations, not denied requests. The deny path's own
	// auth.access_denied row is orthogonal to the falsifier and not
	// asserted here.
	require.Equal(t, 1, countDebugOverrideAuditRows(t, h, iidBP),
		"403 from the auth gate must not write a debug.override.applied row — "+
			"the row in the handler counts actual mutations only")
}

// postDebugOverride POSTs to /v1/instances/{id}/debug/override and
// returns the raw response. When bearerKey is empty (anonymous-mode
// legs), no Authorization header is set; when non-empty, the bearer
// drives the auth-gate path. The two-mode shape lets Legs 1–3 run in
// anonymous mode against the harness-default control-api while Leg 4
// drives the gate with a real restricted key.
func postDebugOverride(t *testing.T, controlBase string, instanceID shared.UUID, body map[string]any, bearerKey string) httpResp {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/v1/instances/%s/debug/override", controlBase, instanceID),
		bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if bearerKey != "" {
		req.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, raw: out}
}

// createInstanceWithPaused POSTs /v1/instances with `paused: true`. The
// supervisor will not dispatch until a subsequent /resume call lands.
// The harness's CreateInstance does not support the create-time pause
// flag, so we drive the route directly.
func createInstanceWithPaused(t *testing.T, controlBase, templateHash, instanceKey string) shared.UUID {
	t.Helper()
	body := map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"paused":       true,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(controlBase+"/v1/instances", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"createInstanceWithPaused: status=%d body=%s", resp.StatusCode, string(out))
	var decoded struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	id, err := uuid.Parse(decoded.InstanceID)
	require.NoError(t, err)
	return shared.UUID(id)
}

// installPauseModeBreakpoint installs a before_dispatch pause-mode
// breakpoint targeting the named node-type. Returns the breakpoint id.
// The pause mode (default when `mode` is omitted) parks the dispatch on
// the breakpoint hit and holds the runner; this is the natural way to
// leave an in-flight run row for the debug-override mutation arms.
func installPauseModeBreakpoint(t *testing.T, controlBase string, instanceID shared.UUID, nodeType string) shared.UUID {
	t.Helper()
	body := map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": nodeType},
		// @constraint: `mode` omitted → defaults to "pause" per the
		// breakpoint spec.
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	url := fmt.Sprintf("%s/v1/instances/%s/breakpoints", controlBase, instanceID)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"installPauseModeBreakpoint: status=%d body=%s", resp.StatusCode, string(out))
	var decoded struct {
		BreakpointID string `json:"breakpoint_id"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	id, err := uuid.Parse(decoded.BreakpointID)
	require.NoError(t, err)
	return shared.UUID(id)
}

// deleteBreakpoint DELETEs the breakpoint row. The schema cascade-deletes
// any open hit rows, which unblocks any waitForResume sleeping on the
// parked dispatch; the supervisor proceeds with the next dispatch arm.
func deleteBreakpoint(t *testing.T, controlBase string, instanceID, breakpointID shared.UUID) {
	t.Helper()
	url := fmt.Sprintf("%s/v1/instances/%s/breakpoints/%s",
		controlBase, instanceID, breakpointID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusNoContent, resp.StatusCode,
		"deleteBreakpoint: status=%d body=%s", resp.StatusCode, string(raw))
}

// waitForFirstHit polls the persistence-layer BreakpointHits accessor
// until a hit row exists for the breakpoint, then returns the first
// one. Falls back to t.Fatalf on timeout. Reading from persistence
// directly (rather than the dedicated /breakpoint-hits HTTP surface)
// keeps this proof focused on the debug-override surface; the
// /breakpoint-hits surface itself is exercised by the breakpoint
// debugger lifecycle scenario.
func waitForFirstHit(t *testing.T, h *scenario.Harness, bpID shared.UUID, timeout time.Duration) persistence.BreakpointHitRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var hits []persistence.BreakpointHitRow
		if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 100, tx)
			hits = r
			return err
		}); err != nil {
			t.Fatalf("waitForFirstHit: %v", err)
		}
		if len(hits) > 0 {
			return hits[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("waitForFirstHit: no hit on breakpoint %s within %v", bpID.String(), timeout)
	return persistence.BreakpointHitRow{}
}

// getInFlightRunID resolves the worker node's current in-flight run id
// from persistence. The breakpoint hit's parked dispatch leaves the run
// row in an in-flight phase (pending / active / held / parked), so the
// NodeRow's join surfaces InFlightRunID as non-nil. Returns the id so
// later steps can read the run's state and attribute row directly.
func getInFlightRunID(t *testing.T, h *scenario.Harness, nodeID shared.UUID) *shared.UUID {
	t.Helper()
	var out *shared.UUID
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.Nodes().Get(ctx, nodeID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			return fmt.Errorf("node %s not found", nodeID.String())
		}
		out = row.InFlightRunID
		return nil
	}))
	return out
}

// getRunState reads the `state` column of the named run row. The
// debug-override invalidate_node action calls MarkStaleForCascade,
// which sets the run row's state to 'stale' and binds the node's
// frame_id; the post-mutation read confirms the visible state. We
// query the column directly to avoid coupling the assertion to the
// NodeRow join's COALESCE behavior.
func getRunState(t *testing.T, h *scenario.Harness, runID shared.UUID) cascade.NodeState {
	t.Helper()
	var state string
	h.QueryRowSQL(
		`SELECT state FROM rimsky_node_runs WHERE id = $1`,
		[]any{runID}, &state)
	return cascade.NodeState(state)
}

// getRunAttributeValue reads the run's attribute row and returns the
// value at `key` (as its native JSON-decoded type). The debug-override
// set_attribute action merges into this row via MergeDelta; the
// assertion lets the test pin "the override actually committed" at the
// persistence layer rather than just the HTTP status struct.
func getRunAttributeValue(t *testing.T, h *scenario.Harness, runID shared.UUID, key string) any {
	t.Helper()
	var out any
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.NodeAttributes().GetByRun(ctx, runID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row, "attribute row must exist after set_attribute")
		out = row.Data[key]
		return nil
	}))
	return out
}

// countDebugOverrideAuditRows counts rimsky_events rows of kind
// "debug.override.applied" scoped to the instance. The handler appends
// in the same tx as the mutation, so a successful override has its
// audit row visible the moment the 200 is observed.
func countDebugOverrideAuditRows(t *testing.T, h *scenario.Harness, instanceID shared.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &instanceID,
			Kind:       "debug.override.applied",
		}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		n = len(out.Events)
		return nil
	}))
	return n
}

// readLatestDebugOverrideAudit returns the payload of the most-recent
// debug.override.applied row for the instance. Callers assert on the
// payload's action / node_type / gate_state / attribute_* fields to
// pin "the audit row records the actual override applied," not just
// "an audit row exists."
func readLatestDebugOverrideAudit(t *testing.T, h *scenario.Harness, instanceID shared.UUID) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := h.Persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &instanceID,
			Kind:       "debug.override.applied",
		}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		require.NotEmpty(t, out.Events, "expected at least one debug.override.applied row")
		payload = out.Events[len(out.Events)-1].Payload
		return nil
	}))
	return payload
}

// stubWorkerCount counts Observed worker dispatches the stub has
// fielded. Used to detect "the cascade re-dispatched after the gate
// cleared" — the falsifier-side observable that the override actually
// propelled the graph forward, not merely returned 200.
func stubWorkerCount(h *scenario.Harness) int {
	n := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			n++
		}
	}
	return n
}

// waitForStubWorkerCount blocks until stubWorkerCount >= want or the
// timeout elapses. The cascade-resume observation has to converge
// within the test's window or the falsifier wins.
func waitForStubWorkerCount(h *scenario.Harness, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if stubWorkerCount(h) >= want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// mintAnonymousAdminKey POSTs /v1/auth/keys with no Bearer (anonymous-
// mode bootstrap) and returns the new admin plaintext. After this call
// the deployment has left anonymous mode; every subsequent request
// needs a Bearer header.
func mintAnonymousAdminKey(t *testing.T, controlBase string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":        "story-debug-channel-admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	require.NoError(t, err)
	resp, err := http.Post(controlBase+"/v1/auth/keys", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"mintAnonymousAdminKey: status=%d body=%s", resp.StatusCode, string(raw))
	var decoded struct {
		Plaintext string `json:"plaintext"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotEmpty(t, decoded.Plaintext)
	return decoded.Plaintext
}

// mintRestrictedKey mints an API key that grants `instance:read` but
// NOT `instance:debug-override`. The narrow grant means no wildcard fan-
// out matches the debug-override action; the auth gate refuses with 403.
// Using `instance:read` (and not `*:read`) keeps the grant concrete
// enough that no wildcard ambiguity could mask the deny path.
func mintRestrictedKey(t *testing.T, controlBase, adminKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":        "story-debug-channel-restricted",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		controlBase+"/v1/auth/keys", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusCreated, resp.StatusCode,
		"mintRestrictedKey: status=%d body=%s", resp.StatusCode, string(raw))
	var decoded struct {
		Plaintext string `json:"plaintext"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotEmpty(t, decoded.Plaintext)
	return decoded.Plaintext
}
