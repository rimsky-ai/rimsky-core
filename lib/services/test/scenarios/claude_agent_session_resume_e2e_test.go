// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-claude-agent-session-resume proof — drives the claude-agent
// executor through THREE in-scope dispatches in one RunScope plus ONE
// sub-graph dispatch in a fresh RunScope against the REAL assembled
// rimsky stack (rimsky-all-in-one image on testcontainers Postgres,
// the real claude-agent executor image with a stub `claude` binary
// that exercises the production CLI runner's --session-id / --resume
// argv paths). The sub-graph dispatch is the load-bearing fresh-
// RunScope case the spec names: build/validate orchestrators rely on
// the sub-graph reset boundary to reason about each pass in isolation,
// so the proof drives the sub-graph code path itself — not an analogous
// cross-instance probe.
//
// What this proves end to end:
//
//  1. **CLI continuity across in-scope dispatches.** The first dispatch
//     spawns the CLI with `--session-id <runId-of-dispatch-1>`. The
//     second and third dispatches resume the CLI with
//     `--resume <session_token>` where `session_token` is the prior
//     dispatch's runId carried forward via the rimsky attribute
//     carry-forward mechanism (per `decision:attribute-carry-forward`
//     and `decision:claude-agent-session-attribute`). Each resumed
//     dispatch is launched on a fake CLI that:
//
//       - keys off `--resume` argv presence as the wire-level signal
//         for "this is a continuation vs. a fresh conversation"
//         (matching real Claude CLI semantics),
//       - reads the prior turn's content from an in-container memory
//         file the first turn wrote, and
//       - writes the observed turn number + the recalled "name" string
//         from the prior turn via `attributes_set`.
//
//     The semantic continuity check is on the recalled "name"
//     (`fake_cli_prior_recall`): if the executor failed to pass
//     `--resume <token>`, the fake CLI would not read the prior file
//     and the recall would be empty.
//
//  2. **A sub-graph invocation starts with a fresh CLI conversation.**
//     The template wires a sub-graph (`subworker`) whose exit is a
//     claude-agent node (`sub_agent`). A `caller` node in the main
//     graph delegates to the sub-graph at instance-creation time. The
//     sub-graph's claude-agent dispatch MUST launch the CLI WITHOUT
//     `--resume` — concept:run-scope blocks carry-forward across the
//     sub-graph boundary, so the schema default (`session_token: ""`)
//     surfaces on the sub_agent's incoming bag regardless of what the
//     main-instance `worker` chain has carried forward.
//
//  3. **The attribute layer pins the carry-forward chain.** For each
//     in-scope main-worker dispatch the test asserts the
//     `rimsky_node_attributes` row carries `session_token` equal to
//     that dispatch's own runId (the platform-side commit of the
//     §12.5 effective bag's stamp). For the sub-graph dispatch the
//     load-bearing observation is the fake CLI's `--resume <token>`
//     argv (`fake_cli_resumed_with`), NOT the exit's own
//     `session_token` row: the sub-graph exit carve-out
//     (concept:sub-graph §"Writeback carry-rule for exit") routes
//     the executor's terminal-final delta — including the
//     platform-stamped session_token — onto the PARENT (caller) run
//     and leaves the exit's own row at its schema defaults.
//
// Falsifier coverage (per spec §STORY-claude-agent-session-resume):
//
//   - "The test uses a fake CLI that does not exercise the real
//     `--resume <token>` path" — the fake CLI's
//     `scenario:session_resume` branch keys EXCLUSIVELY off the
//     `--resume` argv (no parallel mechanism); the spawn-vs-resume
//     decision is the executor's, not the fake CLI's.
//   - "The test does not assert each of the three in-scope dispatches
//     sees the prior turn's context" — the per-turn writeback's
//     `fake_cli_prior_recall` is asserted to be "Alpha" for turns 2
//     and 3 (real recalled content, not a bare `--resume` arg check),
//     and `fake_cli_turn` is asserted as 1, 2, 3 in dispatch order.
//   - "The test does not assert the sub-graph dispatch starts with a
//     fresh CLI conversation" — the sub-graph (`sub_agent`) worker's
//     writeback is asserted to carry `fake_cli_turn: 1`,
//     `fake_cli_prior_recall: ""`, and `fake_cli_resumed_with: ""`
//     (the reset shape the fake CLI produces when launched without
//     `--resume`), AND that dispatch's RunScope is asserted to be a
//     `graph_name='subworker'` scope distinct from the main-instance
//     RunScope (so the test fails loud if the sub-graph carve-out
//     ever silently routes the sub_agent through the parent scope).
//
// Cascade driver: the test sends three operator-source invalidate
// messages (POST /v1/instances/{id}/messages with kind=invalidate,
// target=worker) at the worker. Each message produces a fresh frame
// that re-dispatches the worker within the SAME instance and
// therefore the same RunScope, so carry-forward applies. The sub-
// graph is wired off a `caller` node with no `subscribes:` — the
// initial-frame dispatch path fires it at instance creation; the
// sub-graph's entry executor is absorbed into the caller (the
// rimsky-bundled inproc loop_counter with `max: 1`, which emits
// `event/done` on its single dispatch), and the sub-graph's exit
// `sub_agent` subscribes to that entry's `terminal/*`, so it
// dispatches once in the sub-graph RunScope.
//
// @story: claude-agent-session-resume

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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestClaudeAgentSessionResume drives the full carry-forward +
// sub-graph fresh-RunScope chain end-to-end through the real assembled
// product.
func TestClaudeAgentSessionResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake-session-resume",
		harness.ClaudeAgentFakeOptions{},
	)

	// @deliberate: Postgres backend (not the SQLite default) — the
	// test drives multiple sequential dispatches with message
	// triggers; the SQLite single-writer path has shown
	// non-deterministic dispatch latency on multi-instance sequences.
	rimskyHandle := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("claude-agent", executorEndpoint),
		harness.WithContainerEnv("RIMSKY_LOG_LEVEL", "debug"),
	)
	ep := rimskyHandle.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			rimskyHandle.DumpRimskyLogs(t)
		}
	})

	// @deliberate: open a host-mapped pgxpool against rimsky's state
	// DB to inspect per-dispatch rows + per-dispatch attribute bags.
	// The observability route surfaces only the latest_attributes
	// snapshot; the session-resume proof needs ALL dispatches' bags
	// in dispatch order to pin the carry-forward chain. Direct SQL
	// is the smallest surface that exposes that.
	pgPool := connectStatePostgres(ctx, t, ep.HostDSN)
	t.Cleanup(pgPool.Close)

	tid := deployScenarioTemplate(t, ep, buildSessionResumeTemplate())

	iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-session-resume")
	mainWorkerNodeID := resolveWorkerNodeID(t, ep, iid, "worker")
	subAgentNodeID := resolveWorkerNodeID(t, ep, iid, "sub_agent")

	// @constraint: Acceptance 1 — THREE claude-agent dispatches in
	// ONE RunScope (the main-instance RunScope), each with semantic
	// continuity from the prior turn.
	for i := 1; i <= 3; i++ {
		// @constraint: each operator-source invalidate message opens
		// a new frame that re-dispatches the worker. The
		// Idempotency-Key MUST be unique per emit so each post lands
		// a distinct envelope.
		postWorkerInvalidate(t, ep, iid,
			fmt.Sprintf("session-resume-main-%d", i))
	}

	// @constraint: wait for three settled worker dispatches with
	// non-empty attribute bags. The fake CLI writes session_token +
	// fake_cli_* fields on each turn, so a row whose data is empty
	// (claimed-but-not-yet-completed) does not count.
	waitForWorkerDispatchCount(t, ctx, pgPool, mainWorkerNodeID, 3, 180*time.Second)

	dispatches := getWorkerDispatchesInOrder(t, ctx, pgPool, mainWorkerNodeID)
	if len(dispatches) != 3 {
		t.Fatalf("expected exactly 3 dispatches of `worker` in the main RunScope, got %d (%v)",
			len(dispatches), dispatches)
	}

	// @constraint: turn 1 launches WITHOUT --resume (no prior
	// session_token to carry forward), so the fake CLI resets the
	// log. session_token written = its own runId. prior_recall = "".
	d1 := dispatches[0]
	requireFakeCliTurn(t, d1.attributes, 1)
	requireFakeCliRecall(t, d1.attributes, "")
	requireFakeCliResumedWith(t, d1.attributes, "")
	requireSessionTokenWritten(t, d1.attributes, d1.runID)

	// @constraint: turn 2 — carry-forward MUST surface dispatch-1's
	// runId on this dispatch's incoming attribute bag (the
	// rimsky-platform side); the executor MUST pass that runId on
	// the CLI's --resume argv (the executor-side wiring); the fake
	// CLI MUST recall "Alpha" from dispatch-1's prior-turn log (the
	// CLI-side semantic continuity); session_token gets re-stamped
	// to dispatch-2's runId for the next turn.
	d2 := dispatches[1]
	requireFakeCliTurn(t, d2.attributes, 2)
	requireFakeCliRecall(t, d2.attributes, "Alpha")
	requireFakeCliResumedWith(t, d2.attributes, d1.runID)
	requireSessionTokenWritten(t, d2.attributes, d2.runID)

	// @constraint: turn 3 — same shape as turn 2, but resumed against
	// dispatch-2's runId. prior_recall = "Alpha" again — the chain is
	// unbroken.
	d3 := dispatches[2]
	requireFakeCliTurn(t, d3.attributes, 3)
	requireFakeCliRecall(t, d3.attributes, "Alpha")
	requireFakeCliResumedWith(t, d3.attributes, d2.runID)
	requireSessionTokenWritten(t, d3.attributes, d3.runID)

	// @constraint: all three in-scope dispatches MUST live in the
	// same RunScope — the carry-forward semantic only spans inside
	// one RunScope. A split RunScope shape would mean the cascade
	// re-fire crossed a RunScope boundary mid-loop.
	if d1.runScopeID != d2.runScopeID || d2.runScopeID != d3.runScopeID {
		t.Fatalf("the three main-instance worker dispatches MUST share one RunScope (got %q, %q, %q)",
			d1.runScopeID, d2.runScopeID, d3.runScopeID)
	}

	// @constraint: Acceptance 2 — the sub-graph's claude-agent
	// dispatch starts with a fresh CLI conversation, no carry-forward
	// from the parent (main-instance) RunScope. This is the
	// load-bearing case the spec names: the build/validate
	// orchestrator's "fresh pass" boundary IS the sub-graph RunScope
	// boundary.
	// ----------------------------------------------------------------
	waitForWorkerDispatchCount(t, ctx, pgPool, subAgentNodeID, 1, 180*time.Second)

	subDispatches := getWorkerDispatchesInOrder(t, ctx, pgPool, subAgentNodeID)
	if len(subDispatches) != 1 {
		t.Fatalf("expected exactly 1 dispatch of `sub_agent` in the sub-graph RunScope, got %d (%v)",
			len(subDispatches), subDispatches)
	}
	sub := subDispatches[0]

	// @constraint: the fake CLI reports turn=1 + prior_recall="" —
	// the shape it produces when launched WITHOUT --resume. The
	// resumed_with field is the verbatim `--resume <token>` value,
	// so absence shows as "". These three fields land on the
	// sub_agent's own attribute row via the incremental
	// attributes_set callback path.
	//
	// @concept: sub-graph
	//
	// @deliberate: do NOT assert session_token on the sub_agent's
	// own row. The sub-graph exit carve-out
	// (applyTerminalCompleteSubgraphExit) routes the executor's
	// final-delta writeback — including the platform-stamped
	// `session_token: runId` — onto the PARENT (caller) run's
	// attribute row, NOT the exit's own row ("Writeback carry-rule
	// for exit"). The exit's own row therefore carries only the
	// incremental attributes_set writebacks plus the schema
	// defaults; session_token in that row stays at the schema
	// default "". The session_token property is asserted separately
	// on the main worker dispatches above, where the regular
	// terminal-complete path writes it onto the dispatch's own row
	// (no carry-rule).
	requireFakeCliTurn(t, sub.attributes, 1)
	requireFakeCliRecall(t, sub.attributes, "")
	requireFakeCliResumedWith(t, sub.attributes, "")

	// @concept: run-scope
	//
	// @constraint: the sub-graph dispatch's RunScope MUST be distinct
	// from the main RunScope AND MUST be a `graph_name='subworker'`
	// RunScope. The same node-kind (claude-agent) firing in a
	// DIFFERENT RunScope is the platform-level proof the sub-graph
	// hydration boundary held — fresh-state in a new RunScope is the
	// "Carry-forward boundary" property. The graph_name check guards
	// against a regression where the sub-graph carve-out routed the
	// exit's dispatch through the parent scope
	// (which would silently pass the "different RunScope" check via
	// some other unrelated scope but defeat the actual sub-graph
	// boundary).
	if sub.runScopeID == "" || sub.runScopeID == d1.runScopeID {
		t.Fatalf("sub_agent MUST run in a RunScope distinct from the main worker (main scope=%q, sub scope=%q)",
			d1.runScopeID, sub.runScopeID)
	}
	requireSubgraphRunScope(t, ctx, pgPool, sub.runScopeID, "subworker", iid)
}

// buildSessionResumeTemplate constructs the POST /v1/templates body
// for the session-resume scenario. The template has:
//
//   - Main graph: a claude-agent `worker` subscribed to operator-source
//     invalidate messages targeting itself (each message triggers one
//     in-scope dispatch), and a `caller` node delegating to the
//     `subworker` sub-graph with no `subscribes:` (so the initial-frame
//     dispatch path fires it at instance creation).
//   - Sub-graph `subworker`: entry `sub_trigger` declares the
//     rimsky-bundled inproc loop_counter via the executor alias
//     `rimsky.loop_counter`, with `max: 1` so the single dispatch
//     emits `event/done` AND terminates with Success. The exit is
//     `sub_agent`, a claude-agent node subscribed to `sub_trigger`
//     on `terminal/*`.
//
// Why `executor: rimsky.loop_counter` rather than `kind:
// loop_counter` on the entry — kind sugar is canonicalized AFTER
// the validator's sub-graph flatten pass. The flatten pass absorbs
// the entry's executor onto the calling `caller` node (so the
// calling node dispatches as the entry's executor at runtime), but
// it does not propagate `kind:`. Spelling the executor alias
// directly bypasses that ordering: the alias `rimsky.loop_counter`
// is the same identity the bundled `kind: loop_counter` resolves
// to (per `lib/runtime/executor/builtin/loop_counter`), so the
// runtime dispatch is identical.
//
// The sub-graph entry's executor is absorbed into the caller (per the
// canonical sub-graph identity-and-absorption rule), so the caller's
// runtime dispatch IS the entry's single loop_counter run; that
// terminates, fires `sub_trigger`'s `terminal/*`, and the exit
// `sub_agent` dispatches once in the sub-graph RunScope.
//
// Why this shape rather than driving the sub-graph off the worker's
// terminal cascade — the load-bearing observable is the sub-graph
// hydration boundary (concept:run-scope), NOT the path by which the
// sub-graph was invoked. A no-subscribes delegating caller fires in
// the initial frame so the sub-graph dispatch is observable
// independently of the message-driven worker loop; the two RunScopes
// run in parallel and the assertions can pin both independently.
func buildSessionResumeTemplate() map[string]any {
	// @constraint: agentAttrsFor builds a claude-agent attribute
	// schema whose `user_prompt` default carries the chain-id suffix
	// the fake CLI reads to scope its per-chain memory file. The main
	// worker and the sub-graph worker MUST use distinct chain ids so
	// a fresh-RunScope sub-graph dispatch interleaving with the main
	// worker's resumed dispatches does not reset the main chain's
	// memory file out from under the main chain (a real contamination
	// risk in the fake CLI's container-shared /tmp).
	agentAttrsFor := func(chainID string) map[string]any {
		return map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{
						"type":    "string",
						"default": "claude-sonnet-4-5",
					},
					"system_prompt": map[string]any{
						"type":    "string",
						"default": "you are a session-resume proof stub. follow the scenario hint in the user prompt verbatim.",
					},
					"user_prompt": map[string]any{
						"type":    "string",
						"default": "scenario:session_resume:" + chainID,
					},
					// @constraint: session_token is the carry-forward
					// attribute the claude-agent executor reads to
					// drive --resume. The schema default ("" empty) is
					// what the first dispatch in any RunScope sees —
					// including the sub-graph dispatch, whose
					// sub-graph RunScope has no prior node_run
					// carrying session_token forward.
					"session_token": map[string]any{
						"type":     "string",
						"readOnly": true,
						"default":  "",
					},
					// @deliberate: CLI attribute slot — the executor
					// reads attributes.cli.* for tuning. Empty default
					// keeps the executor on its built-in defaults.
					"cli": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
						"default":    map[string]any{},
					},
				},
			},
		}
	}
	mainAgentAttrs := agentAttrsFor("main")
	subAgentAttrs := agentAttrsFor("sub")

	return map[string]any{
		"spec": map[string]any{
			"name":             "claude-agent-session-resume",
			"version":          "1",
			"frame_timeout_ms": 600000,
			// @deliberate: declared message type the operator POSTs to
			// re-fire the worker. Under messaging's typed-message model
			// the worker subscribes to this message-type as a virtual
			// node; each POST /v1/instances/{id}/messages opens a fresh
			// frame.
			"messages": []map[string]any{
				{
					"type": "operator/worker-rerun",
					"body_schema": map[string]any{
						"type": "object",
					},
				},
			},
			"graphs": []map[string]any{
				{
					"name": "main",
					"nodes": []map[string]any{
						{
							"type":       "worker",
							"executor":   "claude-agent",
							"attributes": mainAgentAttrs,
							"subscribes": []map[string]any{
								{
									// @deliberate: subscribe to the
									// `operator/worker-rerun` message
									// as a virtual node. Each POST
									// /v1/instances/{id}/messages of
									// that type opens a fresh frame
									// that re-dispatches the worker in
									// the same instance's RunScope, so
									// carry-forward surfaces the prior
									// dispatch's session_token on
									// every post-first turn.
									"node":                   "operator/worker-rerun",
									"type":                   "terminal/success",
									"wake_on_change":         true,
									"force_upstream_refresh": false,
								},
							},
						},
						{
							// @deliberate: caller delegates to the
							// sub-graph at instance creation (no
							// `subscribes:` ⇒ initial frame). The
							// sub-graph entry's executor (`kind:
							// loop_counter` with max=1) gets absorbed
							// into this node by the template
							// canonicalizer. The caller's
							// run is the single loop_counter dispatch;
							// on terminal Success it emits the
							// internal-cascade through `sub_trigger`'s
							// terminal/*.
							"type":     "caller",
							"delegate": "subworker",
						},
					},
				},
				{
					"name":  "subworker",
					"entry": "sub_trigger",
					"exit":  "sub_agent",
					"nodes": []map[string]any{
						{
							// @deliberate: sub-graph entry — gets
							// absorbed into the main-graph `caller`
							// node by the canonical
							// identity-and-absorption rule. `executor:
							// rimsky.loop_counter` is the bundled
							// inproc loop_counter's executor alias
							// (the same identity `kind: loop_counter`
							// resolves to); using the alias directly
							// sidesteps the kind-sugar / absorption
							// ordering (see the docstring at
							// buildSessionResumeTemplate). max=1 means
							// the single dispatch emits `event/done`
							// AND terminates with Success — both
							// events flow through the canonical
							// sub-graph internal cascade to fire the
							// exit.
							"type":     "sub_trigger",
							"executor": "rimsky.loop_counter",
							"attributes": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"max": map[string]any{
											"type":    "integer",
											"default": 1,
										},
									},
								},
							},
						},
						{
							// @concept: run-scope
							//
							// @deliberate: sub-graph exit — the
							// claude-agent dispatch the test asserts
							// on. Its dispatch runs in the sub-graph
							// RunScope (graph_name='subworker') which
							// blocks carry-forward across that
							// boundary, so the schema default
							// (session_token: "") surfaces here
							// regardless of what the main-instance
							// worker chain carried forward.
							"type":       "sub_agent",
							"executor":   "claude-agent",
							"attributes": subAgentAttrs,
							"subscribes": []map[string]any{
								{
									"node":                   "sub_trigger",
									"type":                   "terminal/*",
									"wake_on_change":         true,
									"force_upstream_refresh": false,
								},
							},
						},
					},
				},
			},
		},
	}
}

// postWorkerInvalidate posts one operator-source invalidate message
// targeting `worker` against the given instance. The Idempotency-Key
// is the per-post key the rimsky API requires (per cfg:messages and
// CLAUDE.md §"Universal Idempotency-Key header on POST /instances/
// {id}/messages"). A 201 Created confirms the envelope was inserted
// and a fresh frame opens for the worker; a 200 OK would indicate a
// dedup replay (caller passed a duplicate key and the test would
// observe fewer dispatches than expected).
func postWorkerInvalidate(t *testing.T, ep harness.RimskyEndpoint, instanceID, idempotencyKey string) {
	t.Helper()
	body := map[string]any{
		"type":    "operator/worker-rerun",
		"payload": map[string]any{},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal invalidate body: %v", err)
	}
	path := "/v1/instances/" + instanceID + "/messages"
	req, err := http.NewRequest(http.MethodPost, ep.BaseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("build POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s with Idempotency-Key %q returned %d, want 201 Created\nbody: %s",
			path, idempotencyKey, resp.StatusCode, string(raw))
	}
}

// workerDispatch is one dispatch of a claude-agent node. Fields are
// the per-dispatch runId, its RunScope, and the persisted attribute
// bag the dispatch's writeback produced.
type workerDispatch struct {
	runID      string
	runScopeID string
	attributes map[string]any
}

// connectStatePostgres opens a pgxpool against the host-mapped DSN so
// the test process can SELECT per-dispatch attribute bags directly.
func connectStatePostgres(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	if hostDSN == "" {
		t.Fatalf("HostDSN empty; the session-resume proof requires Postgres backend (WithSQLite is incompatible with the direct SQL probe)")
	}
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("connect rimsky state postgres: %v", err)
	}
	return pool
}

// getWorkerDispatchesInOrder returns the SETTLED dispatches of
// `nodeID` ordered by enqueue time, with each dispatch's runId +
// RunScope + persisted attribute bag (from rimsky_node_attributes
// joined on node_run_id). Only rows with a non-null settling
// signal type are surfaced — pending / claimed rows would carry an
// empty attribute bag and skew the assertions.
func getWorkerDispatchesInOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) []workerDispatch {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT nr.id::text, nr.run_scope_id::text, COALESCE(na.data, '{}'::jsonb)
		  FROM rimsky_node_runs nr
		  LEFT JOIN rimsky_node_attributes na ON na.node_run_id = nr.id
		 WHERE nr.node_id = $1::uuid
		   AND nr.settling_signal_type IS NOT NULL
		 ORDER BY nr.enqueued_at, nr.id
	`, nodeID)
	if err != nil {
		t.Fatalf("query rimsky_node_runs for node %s: %v", nodeID, err)
	}
	defer rows.Close()
	out := []workerDispatch{}
	for rows.Next() {
		var runID, scopeID string
		var dataRaw []byte
		if err := rows.Scan(&runID, &scopeID, &dataRaw); err != nil {
			t.Fatalf("scan rimsky_node_runs row: %v", err)
		}
		var attrs map[string]any
		if err := json.Unmarshal(dataRaw, &attrs); err != nil {
			t.Fatalf("decode rimsky_node_attributes.data for run %s: %v: %s",
				runID, err, string(dataRaw))
		}
		if attrs == nil {
			attrs = map[string]any{}
		}
		out = append(out, workerDispatch{
			runID:      runID,
			runScopeID: scopeID,
			attributes: attrs,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rimsky_node_runs: %v", err)
	}
	return out
}

// waitForWorkerDispatchCount polls until `nodeID` has at least N
// settled dispatches. A timeout fatals the test with the current
// observed count + last per-dispatch state.
func waitForWorkerDispatchCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string, wantN int, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	lastN := 0
	for time.Now().Before(end) {
		out := getWorkerDispatchesInOrder(t, ctx, pool, nodeID)
		lastN = len(out)
		if lastN >= wantN {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("node %s did not reach %d settled dispatches within %v (last count=%d)",
		nodeID, wantN, deadline, lastN)
}

// requireSubgraphRunScope asserts the rimsky_run_scopes row for
// `scopeID` lives in `instanceID` AND carries `graph_name=wantGraph`.
// This is the load-bearing check that the sub_agent's dispatch ran
// in the sub-graph's own RunScope — concept:run-scope creates a new
// row with graph_name set to the sub-graph's name when a caller
// delegates. A graph_name='main' here would mean the sub-graph
// carve-out silently routed the exit through the parent scope,
// defeating the whole carry-forward boundary the story pins.
func requireSubgraphRunScope(t *testing.T, ctx context.Context, pool *pgxpool.Pool, scopeID, wantGraph, instanceID string) {
	t.Helper()
	var gotGraph, gotInstance string
	err := pool.QueryRow(ctx, `
		SELECT graph_name, instance_id::text
		  FROM rimsky_run_scopes
		 WHERE id = $1::uuid
	`, scopeID).Scan(&gotGraph, &gotInstance)
	if err != nil {
		t.Fatalf("query rimsky_run_scopes for scope %s: %v", scopeID, err)
	}
	if gotInstance != instanceID {
		t.Fatalf("sub_agent RunScope %s belongs to instance %s, want %s",
			scopeID, gotInstance, instanceID)
	}
	if gotGraph != wantGraph {
		t.Fatalf("sub_agent RunScope %s graph_name=%q, want %q — the sub-graph carve-out did not route the exit through the sub-graph's own RunScope",
			scopeID, gotGraph, wantGraph)
	}
}

// requireFakeCliTurn asserts the persisted bag carries
// `fake_cli_turn` equal to the expected value. fake_cli_turn is
// written by the fake CLI's session_resume scenario via attributes_set
// AFTER reading the in-container memory file — its value is the
// turn the fake CLI observed AT DISPATCH TIME, so the assertion
// pins both the dispatch sequencing AND the carry-forward chain
// (a missing prior file would have failed the fake CLI loudly).
func requireFakeCliTurn(t *testing.T, attrs map[string]any, want int) {
	t.Helper()
	if attrs == nil {
		t.Fatalf("attributes nil; cannot read fake_cli_turn (want=%d)", want)
	}
	got, ok := attrs["fake_cli_turn"]
	if !ok {
		t.Fatalf("attributes missing fake_cli_turn (want=%d): %v", want, attrs)
	}
	gotInt, err := asJSONInt(got)
	if err != nil {
		t.Fatalf("fake_cli_turn of unexpected type %T (%v): %v", got, got, err)
	}
	if gotInt != want {
		t.Fatalf("fake_cli_turn = %d, want %d (attrs: %v)", gotInt, want, attrs)
	}
}

// requireFakeCliRecall asserts the persisted bag carries
// `fake_cli_prior_recall` equal to the expected value. The fake CLI
// writes this field from the prior-turn memory file on resume, or
// "" when launched without --resume — the assertion is the
// semantic-continuity check (per the Falsifier: "the test does not
// assert each of the three in-scope dispatches sees the prior
// turn's context").
func requireFakeCliRecall(t *testing.T, attrs map[string]any, want string) {
	t.Helper()
	got, _ := attrs["fake_cli_prior_recall"].(string)
	if got != want {
		t.Fatalf("fake_cli_prior_recall = %q, want %q (attrs: %v)", got, want, attrs)
	}
}

// requireFakeCliResumedWith asserts the persisted bag's
// `fake_cli_resumed_with` field — the verbatim value the fake CLI
// observed on its `--resume <token>` argv (or "" when --resume was
// absent). The expected value on turn N+1 is the runId of dispatch
// N (the executor's carry-forward chain), and the expected value on
// turn 1 / sub-graph is "".
func requireFakeCliResumedWith(t *testing.T, attrs map[string]any, want string) {
	t.Helper()
	got, _ := attrs["fake_cli_resumed_with"].(string)
	if got != want {
		t.Fatalf("fake_cli_resumed_with = %q, want %q (attrs: %v)", got, want, attrs)
	}
}

// requireSessionTokenWritten asserts the persisted bag's
// `session_token` equals the dispatch's own runId — this is the
// load-bearing post-condition `agent-run.ts::onComplete` pins
// (every Success stamps `session_token: runId` on the effective
// bound bag).
func requireSessionTokenWritten(t *testing.T, attrs map[string]any, wantRunID string) {
	t.Helper()
	got, _ := attrs["session_token"].(string)
	if got != wantRunID {
		t.Fatalf("session_token = %q, want %q (the dispatch's own runId) (attrs: %v)",
			got, wantRunID, attrs)
	}
}

// asJSONInt coerces a JSON-decoded numeric value to int. JSON numbers
// decode as float64 through encoding/json; the fake CLI writes
// fake_cli_turn as a number, so the persisted form is float64 here.
func asJSONInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
