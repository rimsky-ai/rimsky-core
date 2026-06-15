// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Acceptance gate for STORY-node-admin (spec
// 2026-06-08-design-corpus-bootstrap). The operator-facing
// invalidate routes (`POST /v1/nodes/{id}/invalidate` and the admin
// double) are retired — invalidate is now expressed by posting a
// typed message via `POST /v1/instances/{instance_id}/messages` (or
// ad-hoc force-stale via `POST /v1/debug/override`) — so this file's
// scope shrinks to the two remaining node-admin legs the story
// names:
//
//  1. GET — `GET /v1/nodes/{id}` returns the node's full state +
//     settling signal type, observable through the real control-api
//     against the assembled product.
//
//  2. Reset — `POST /v1/nodes/{id}/reset` on a node driven to a real
//     failed terminal via an exhausted retry-then-give_up policy
//     clears the persisted error counters (current_error_class +
//     retry_counter) AND the supervisor genuinely re-dispatches the
//     node (the falsifier guards against "reset clears the visible
//     counter but the supervisor still treats the node as
//     exhausted"). The error counter clearing is observed THROUGH
//     `GET /v1/nodes/{id}` — the same operator-facing surface the
//     story names — not via raw SQL, so the proof exhibits the user
//     outcome through the real surface.
//
// The proof drives the real assembled product: real control-api over
// HTTP, real scheduler + frame engine, real supervisor + stub
// executor dispatch, testcontainers Postgres. No hand-rolled state —
// the failed terminal comes from the policy chain genuinely
// exhausting through real dispatches.
package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	graphshared "github.com/rimsky-ai/rimsky-core/lib/graph/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// nodeDetailResponse mirrors the (subset of) JSON shape the
// `GET /v1/nodes/{id}` handler emits — only the fields this test
// load-bears on are declared. Matching the handler's `nodeResponse`
// json tags ensures the assertion is reading the same wire shape an
// operator would see, not a parallel projection.
type nodeDetailResponse struct {
	ID                 string `json:"id"`
	InstanceID         string `json:"instance_id"`
	NodeType           string `json:"node_type"`
	State              string `json:"state"`
	SettlingSignalType string `json:"settling_signal_type,omitempty"`
	CurrentErrorClass  string `json:"current_error_class,omitempty"`
	RetryCounter       int    `json:"retry_counter"`
	ActionIndex        int    `json:"action_index"`
}

// TestAcceptance_NodeAdmin_GetAndReset drives STORY-node-admin's
// get / reset legs through the real assembled product. The retired
// operator-invalidate route is no longer in scope (invalidate is
// expressed via typed-message POST or debug-override).
func TestAcceptance_NodeAdmin_GetAndReset(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @deliberate: Two distinct node types so each leg's target is independent:
	//   `worker` settles fresh on every dispatch (drives the get leg).
	//   `flaky` errors with a `give_up`-terminated policy chain
	//      (drives the reset leg's real-failed-terminal seed).
	// Re-scripting `flaky` to succeed before reset proves the
	// supervisor re-fires the node — if reset only cleared the
	// visible counter and the supervisor still treated it as
	// exhausted, the re-fire would never produce a `terminal/success`
	// signal, and the post-reset state poll would time out.
	h.Stub.WhenType("worker").Success(map[string]any{"w": 1}, true, "worker")
	h.Stub.WhenType("flaky").Error("my_err", map[string]any{"hint": "boom"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "node-admin", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "worker", Executor: "stub",
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "flaky", Executor: "stub",
				// @deliberate: Two retries then give_up — drives the node to
				// state=failed through the real policy chain (the
				// same shape as `give_up_test.go`). Two retries
				// makes retry_counter genuinely > 0 by the time the
				// node reaches failed, so the reset's clear-the-
				// counter assertion has something to clear.
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/my_err": {
						Policy: []node.PolicyAction{
							{
								Action:      "retry",
								Count:       2,
								Backoff:     graphshared.BackoffExponential,
								BaseDelayMs: 50,
								MaxDelayMs:  200,
							},
							{Action: "give_up"},
						},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-node-admin", map[string]any{})

	worker := h.FindNode(iid, "worker")
	flaky := h.FindNode(iid, "flaky")
	require.NotNil(t, worker)
	require.NotNil(t, flaky)

	// @constraint: Drive both nodes to their first terminal via the real
	// supervisor.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 30*time.Second),
		"worker must settle fresh on its first real dispatch")
	require.True(t, h.WaitForNodeState(flaky.ID, cascade.NodeStateFailed, 30*time.Second),
		"flaky must reach failed via retry-then-give_up — without a real failed terminal the reset leg's "+
			"falsifier (counter cleared but supervisor still treats node as exhausted) is untestable")

	// @deliberate: Leg 1: GET /v1/nodes/{id} surfaces full state
	//
	// The story's Acceptance: "an operator retrieves a node and sees
	// its current state and settling signal type." The worker is
	// settled-fresh through the real dispatch path, so the handler's
	// projection of the node row must reflect that state and the
	// run's settling_signal_type column must project as the
	// canonical `terminal/success` signal type-path.
	workerDetail := getNodeDetail(t, h, worker.ID)
	require.Equal(t, worker.ID.String(), workerDetail.ID,
		"GET /v1/nodes/{id} must surface the id the operator queried")
	require.Equal(t, "fresh", workerDetail.State,
		"settled worker must project state=fresh through GET /v1/nodes/{id}")
	require.Equal(t, "terminal/success", workerDetail.SettlingSignalType,
		"GET /v1/nodes/{id} must surface the settling_signal_type column projected via NodeRow — "+
			"omitting it would fail the story's 'sees its current settling signal type' clause")

	// @constraint: The flaky node is at a real failed terminal; the handler's
	// projection must surface the persisted error bookkeeping —
	// current_error_class names which class is owed (the cursor into
	// the policy chain) and action_index records how far the chain
	// has advanced. These are the operator's window into the node's
	// "error budget exhaustion" state and the same fields the reset
	// leg will clear. Pinning them here guards against the spec's
	// falsifier ("reset clears the visible counter…") being silently
	// satisfied because the counter was never observable in the
	// first place. (The chain-step retry_counter is reset to 0 at
	// give_up per `node/policy.go::step`; the persisted
	// "exhaustion" markers an operator actually sees are
	// current_error_class + action_index.)
	flakyDetail := getNodeDetail(t, h, flaky.ID)
	require.Equal(t, "failed", flakyDetail.State,
		"failed flaky must project state=failed through GET /v1/nodes/{id}")
	require.Equal(t, "stub/my_err", flakyDetail.CurrentErrorClass,
		"GET /v1/nodes/{id} must surface current_error_class at the failed terminal — "+
			"this is the operator-visible 'budget exhausted' marker the reset leg clears")
	require.Greater(t, flakyDetail.ActionIndex, 0,
		"flaky must carry action_index > 0 at failed terminal (its policy chain advanced past the "+
			"`retry` action to `give_up`) — an action_index of 0 means the chain never advanced, and "+
			"the reset leg's 'budget cleared' assertion would be vacuous")

	// @deliberate: 404 path on the GET surface (the story's read is a real
	// read-or-404 — silent canned responses are the falsifier here).
	notFoundResp, err := http.Get(h.ControlBase + "/v1/nodes/00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	notFoundResp.Body.Close()
	require.Equal(t, http.StatusNotFound, notFoundResp.StatusCode,
		"GET /v1/nodes/{unknown} must 404 — silently returning 200 with a stub response is the falsifier")

	// @deliberate: Leg 2: POST /v1/nodes/{id}/reset
	//
	// The story's Acceptance: "resetting a failed node clears its
	// error count and the next acquisition attempt is not skipped
	// due to error budget exhaustion." The flaky node is at a real
	// failed terminal with retry_counter > 0 (asserted above via
	// GET). Re-script the stub to succeed before reset so the
	// supervisor's re-dispatch can reach terminal — if reset only
	// cleared the visible counter but the supervisor still treated
	// the node as exhausted, the re-dispatch would never produce a
	// `terminal/success` for `flaky`.
	h.Stub.WhenType("flaky").Success(map[string]any{"f": 1}, true, "flaky")

	preResetSuccessCount := countTerminalSuccess(t, h, flaky.ID)
	require.Equal(t, 0, preResetSuccessCount,
		"flaky must have zero successful terminals before reset (only failures so far)")

	resetResp, err := http.Post(
		h.ControlBase+"/v1/nodes/"+flaky.ID.String()+"/reset",
		"application/json", bytes.NewReader([]byte(`{}`)),
	)
	require.NoError(t, err)
	resetResp.Body.Close()
	require.Equal(t, http.StatusOK, resetResp.StatusCode,
		"POST /v1/nodes/{id}/reset on a failed node must return 200")

	// @deliberate: Discriminator 1 (error budget cleared, observable via the
	// operator-facing GET surface): the story explicitly names this
	// as the user-observable proof — "observable via `GET /v1/nodes/{id}`"
	// per the plan's task 21 step 3. The cleared fields are
	// current_error_class (no class owed) and action_index (chain
	// cursor reset to 0). The retry_counter is also written to 0,
	// but it was already 0 at the failed terminal (give_up resets
	// it per `node/policy.go::step`), so the chain cursor is the
	// load-bearing observable here.
	//
	// Poll the GET surface until the budget markers clear; the
	// handleResetNode tx (UpdateError with a zero-value EvaluatorState)
	// writes synchronously with the OK response, so this should
	// observe on the first poll — but using the poll loop keeps the
	// test robust against the supervisor enqueuing the next dispatch
	// concurrently and re-writing the error fields if the executor
	// errored before the test's re-script lands (the re-script lands
	// before the reset POST so that race is closed too).
	require.True(t, waitForClearedErrorBudget(t, h, flaky.ID, 10*time.Second),
		"after POST /reset, GET /v1/nodes/{id} must surface action_index=0 AND current_error_class='' — "+
			"the falsifier's 'reset clears the visible counter' shape is satisfied here, but only the "+
			"next discriminator proves the supervisor isn't still treating the node as exhausted")

	// @deliberate: Discriminator 2 (the supervisor really re-fires the node — the
	// falsifier's 'supervisor still treats the node as exhausted'
	// shape is closed by observing a REAL successful terminal on
	// the now-rescripted `flaky`):
	require.True(t, waitForTerminalSuccessCountGreaterThan(t, h, flaky.ID, preResetSuccessCount, 30*time.Second),
		"after POST /reset, the supervisor must genuinely re-dispatch the flaky node — "+
			"a `terminal/success` signal must arrive against the rescripted-success stub. "+
			"The falsifier's 'reset clears the visible counter but the supervisor still treats the "+
			"node as exhausted' shape leaves the count at zero forever (the next-dispatch is silently "+
			"skipped despite the cleared counter).")
	require.True(t, h.WaitForNodeState(flaky.ID, cascade.NodeStateFresh, 10*time.Second),
		"flaky must reach fresh after reset + supervisor re-dispatch — the full re-fire cycle "+
			"completing end to end is the story's terminal observation")

	// @deliberate: Final post-condition: the cleared error budget persists past
	// the successful re-fire (a fresh node carries no error
	// bookkeeping), confirming the reset's clear-and-re-fire chain
	// is coherent end to end.
	finalDetail := getNodeDetail(t, h, flaky.ID)
	require.Equal(t, 0, finalDetail.ActionIndex,
		"after reset + successful re-fire, action_index stays cleared (a fresh node has no policy-chain cursor)")
	require.Equal(t, 0, finalDetail.RetryCounter,
		"after reset + successful re-fire, retry_counter stays cleared (a fresh node has no retries pending)")
	require.Equal(t, "", finalDetail.CurrentErrorClass,
		"after reset + successful re-fire, current_error_class stays cleared (no active error class)")
}

// getNodeDetail issues GET /v1/nodes/{id} against the real
// control-api and decodes the response into the test-local
// projection. Fatals on non-200 / decode error — the assertions
// inside the test then check the field values.
func getNodeDetail(t *testing.T, h *scenario.Harness, nodeID shared.UUID) nodeDetailResponse {
	t.Helper()
	resp, err := http.Get(h.ControlBase + "/v1/nodes/" + nodeID.String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /v1/nodes/{id} must return 200")
	var out nodeDetailResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// countTerminalSuccess returns the number of `terminal/success`
// events currently persisted for the node. Used as a baseline to
// observe genuine NEW dispatches (counter grew) versus the
// falsifier's "supervisor never picked the node up" shape (counter
// stayed at the baseline).
func countTerminalSuccess(t *testing.T, h *scenario.Harness, nodeID shared.UUID) int {
	t.Helper()
	var n int
	h.QueryRowSQL(`
        SELECT count(*) FROM rimsky_events
         WHERE node_id = $1 AND kind = 'terminal/success'
    `, []any{nodeID}, &n)
	return n
}

// waitForTerminalSuccessCountGreaterThan polls until the
// `terminal/success` event count for the node strictly exceeds
// `baseline`, or times out. This is the rigorous shape for "the
// supervisor genuinely re-fired the node on a real dispatch" — a
// new success event is only persisted by the real terminal-
// resolution path, which only runs after a real dispatch lands.
func waitForTerminalSuccessCountGreaterThan(t *testing.T, h *scenario.Harness, nodeID shared.UUID, baseline int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countTerminalSuccess(t, h, nodeID) > baseline {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// waitForClearedErrorBudget polls GET /v1/nodes/{id} until the
// projected action_index is zero AND the projected
// current_error_class is empty, or times out. Observing the cleared
// fields THROUGH the operator-facing GET surface (rather than via
// raw SQL on rimsky_nodes) keeps the proof faithful to the story:
// the operator sees the cleared budget markers via the same route
// the story names. The chain cursor (action_index) is the load-
// bearing observable — at the failed terminal action_index > 0
// (the chain advanced past `retry` to `give_up`); the reset writes
// a zero-value EvaluatorState which resets it to 0.
func waitForClearedErrorBudget(t *testing.T, h *scenario.Harness, nodeID shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d := getNodeDetail(t, h, nodeID)
		if d.ActionIndex == 0 && d.CurrentErrorClass == "" {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
