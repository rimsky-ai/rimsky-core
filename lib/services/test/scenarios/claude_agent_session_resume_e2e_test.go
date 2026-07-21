// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestClaudeAgentSessionResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	executorEndpoint := harness.StartClaudeAgentFakeOnNetwork(
		ctx, t, netName, "claude-agent-fake-session-resume",
		harness.ClaudeAgentFakeOptions{},
	)
	stubEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	rimskyHandle := harness.BringUpRimskyHandle(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("claude-agent", executorEndpoint),
		harness.WithExecutor("stub", stubEndpoint),
		harness.WithContainerEnv("RIMSKY_LOG_LEVEL", "debug"),
	)
	ep := rimskyHandle.Endpoint
	t.Cleanup(func() {
		if t.Failed() {
			rimskyHandle.DumpRimskyLogs(t)
		}
	})

	pgPool := connectStatePostgres(ctx, t, ep.HostDSN)
	t.Cleanup(pgPool.Close)

	tid := deployScenarioTemplate(t, ep, buildSessionResumeTemplate())
	iid := createScenarioInstance(t, ep, tid, "ck-claude-agent-session-resume")
	workerNodeID := resolveWorkerNodeID(t, ep, iid, "worker")
	subWorkerNodeID := resolveWorkerNodeID(t, ep, iid, "sub-worker")
	callerNodeID := resolveWorkerNodeID(t, ep, iid, "caller")

	postWorkerInvalidate(t, ep, iid, "session-resume-loop")

	waitForWorkerDispatchCount(t, ctx, pgPool, workerNodeID, 3, 180*time.Second)
	waitForNodeRunsQuiescent(t, ctx, pgPool, workerNodeID, 30*time.Second)

	dumpWorkerDispatchProvenance(t, ctx, pgPool, workerNodeID)

	if got := countNodeRuns(t, ctx, pgPool, workerNodeID); got != 3 {
		t.Fatalf("worker node %s has %d total node_run rows after going dispatch-quiescent, want "+
			"exactly 3 — a broken `when: payload.attributes_delta.fake_cli_turn < 3` subscription "+
			"filter would enqueue a 4th dispatch even before it settles", workerNodeID, got)
	}

	dispatches := getWorkerDispatchesInOrder(t, ctx, pgPool, workerNodeID)
	if len(dispatches) != 3 {
		t.Fatalf("expected exactly 3 worker dispatches in one frame's RunScope, got %d (%v)",
			len(dispatches), dispatches)
	}

	d1 := dispatches[0]
	requireFakeCliTurn(t, d1.attributes, 1)
	requireFakeCliRecall(t, d1.attributes, "")
	requireFakeCliResumedWith(t, d1.attributes, "")
	requireSessionTokenWritten(t, d1.attributes, d1.runID)

	d2 := dispatches[1]
	requireFakeCliTurn(t, d2.attributes, 2)
	requireFakeCliRecall(t, d2.attributes, "Alpha")
	requireFakeCliResumedWith(t, d2.attributes, d1.runID)
	requireSessionTokenWritten(t, d2.attributes, d2.runID)

	d3 := dispatches[2]
	requireFakeCliTurn(t, d3.attributes, 3)
	requireFakeCliRecall(t, d3.attributes, "Alpha")
	requireFakeCliResumedWith(t, d3.attributes, d2.runID)
	requireSessionTokenWritten(t, d3.attributes, d3.runID)

	if d1.runScopeID != d2.runScopeID || d2.runScopeID != d3.runScopeID {
		t.Fatalf("the three worker dispatches MUST share one RunScope (got %q, %q, %q); carry-forward is intra-RunScope and the cascade self-edge keeps the loop inside one frame's RunScope",
			d1.runScopeID, d2.runScopeID, d3.runScopeID)
	}

	waitForWorkerDispatchCount(t, ctx, pgPool, subWorkerNodeID, 1, 120*time.Second)
	dumpWorkerDispatchProvenance(t, ctx, pgPool, subWorkerNodeID)
	dumpWorkerDispatchProvenance(t, ctx, pgPool, callerNodeID)

	subDispatches := getWorkerDispatchesInOrder(t, ctx, pgPool, subWorkerNodeID)
	if len(subDispatches) != 1 {
		t.Fatalf("expected exactly 1 sub-worker dispatch inside the sub-graph invocation, got %d (%v)",
			len(subDispatches), subDispatches)
	}
	subD := subDispatches[0]

	if subD.runScopeID == d3.runScopeID {
		t.Fatalf("sub-graph worker dispatch shares the parent frame's RunScope (%q) with the three-turn parent worker — "+
			"a sub-graph invocation must run in its own RunScope, not inherit the calling frame's",
			subD.runScopeID)
	}

	callerDispatches := getWorkerDispatchesInOrder(t, ctx, pgPool, callerNodeID)
	if len(callerDispatches) != 1 {
		t.Fatalf("expected exactly 1 caller dispatch owning the sub-graph invocation, got %d (%v)",
			len(callerDispatches), callerDispatches)
	}
	callerAttrs := callerDispatches[0].attributes
	requireFakeCliTurn(t, callerAttrs, 1)
	requireFakeCliRecall(t, callerAttrs, "")
	requireFakeCliResumedWith(t, callerAttrs, "")
	requireSessionTokenWritten(t, callerAttrs, subD.runID)
}

func buildSessionResumeTemplate() map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    "claude-agent-session-resume",
			"version": "1",
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
							"attributes": sessionResumeAgentAttrs("scenario:session_resume:main"),
							"subscribes": []map[string]any{
								{
									"node":                   "operator/worker-rerun",
									"type":                   "terminal/success",
									"force_upstream_refresh": false,
								},
								{
									"node":                   "worker",
									"type":                   "terminal/success",
									"when":                   "payload.attributes_delta.fake_cli_turn < 3",
									"force_upstream_refresh": false,
								},
							},
						},
						{
							"type":     "caller",
							"delegate": "reset-check",
							"subscribes": []map[string]any{
								{
									"node":                   "worker",
									"type":                   "terminal/success",
									"when":                   "payload.attributes_delta.fake_cli_turn == 3",
									"force_upstream_refresh": false,
								},
							},
						},
					},
				},
				{
					"name":  "reset-check",
					"entry": "sub-entry",
					"exit":  "sub-worker",
					"nodes": []map[string]any{
						{
							"type":     "sub-entry",
							"executor": "stub",
						},
						{
							"type":       "sub-worker",
							"executor":   "claude-agent",
							"attributes": sessionResumeAgentAttrs("scenario:session_resume:main"),
							"subscribes": []map[string]any{
								{
									"node":                   "sub-entry",
									"type":                   "terminal/*",
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

func sessionResumeAgentAttrs(userPrompt string) map[string]any {
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
					"default": userPrompt,
				},
				"session_token": map[string]any{
					"type":     "string",
					"readOnly": true,
					"default":  "",
				},
				"cli": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
					"default":    map[string]any{},
				},
			},
		},
	}
}

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

type workerDispatch struct {
	runID      string
	runScopeID string
	attributes map[string]any
}

func dumpWorkerDispatchProvenance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT nr.id::text, nr.run_scope_id::text, nr.frame_id::text, nr.sequence, nr.state,
		       nr.creation_reason, nr.enqueued_at,
		       COALESCE(na.dispatch_input_bag, '{}'::jsonb), COALESCE(na.data, '{}'::jsonb)
		  FROM rimsky_node_runs nr
		  LEFT JOIN rimsky_node_attributes na ON na.node_run_id = nr.id
		 WHERE nr.node_id = $1::uuid
		 ORDER BY nr.sequence
	`, nodeID)
	if err != nil {
		t.Logf("dumpWorkerDispatchProvenance: query: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var runID, scopeID, frameID, state, creationReason string
		var seq int64
		var enqueuedAt time.Time
		var inputBag, data []byte
		if err := rows.Scan(&runID, &scopeID, &frameID, &seq, &state, &creationReason, &enqueuedAt, &inputBag, &data); err != nil {
			t.Logf("dumpWorkerDispatchProvenance: scan: %v", err)
			return
		}
		t.Logf("  dispatch run=%s seq=%d frame=%s scope=%s state=%s creation_reason=%s enqueued_at=%s",
			runID, seq, frameID, scopeID, state, creationReason, enqueuedAt.Format(time.RFC3339Nano))
		t.Logf("    dispatch_input_bag=%s", string(inputBag))
		t.Logf("    data=%s", string(data))
	}
}

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

func getWorkerDispatchesInOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) []workerDispatch {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT nr.id::text, nr.run_scope_id::text, COALESCE(na.data, '{}'::jsonb)
		  FROM rimsky_node_runs nr
		  LEFT JOIN rimsky_node_attributes na ON na.node_run_id = nr.id
		 WHERE nr.node_id = $1::uuid
		   AND nr.settling_signal_type IS NOT NULL
		 ORDER BY nr.sequence
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

func countNodeRuns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1::uuid`, nodeID,
	).Scan(&n); err != nil {
		t.Fatalf("count node_runs for %s: %v", nodeID, err)
	}
	return n
}

func waitForNodeRunsQuiescent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string, deadline time.Duration) {
	t.Helper()
	const stableWindow = 5 * time.Second
	end := time.Now().Add(deadline)
	last := countNodeRuns(t, ctx, pool, nodeID)
	stableSince := time.Now()
	for time.Now().Before(end) {
		time.Sleep(500 * time.Millisecond)
		cur := countNodeRuns(t, ctx, pool, nodeID)
		if cur != last {
			last = cur
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= stableWindow {
			return
		}
	}
	dumpWorkerDispatchProvenance(t, ctx, pool, nodeID)
	t.Fatalf("node %s never went dispatch-quiescent within %v (node_run row count kept changing, last=%d)",
		nodeID, deadline, last)
}

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
	dumpWorkerDispatchProvenance(t, ctx, pool, nodeID)
	t.Fatalf("node %s did not reach %d settled dispatches within %v (last count=%d)",
		nodeID, wantN, deadline, lastN)
}

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

func requireFakeCliRecall(t *testing.T, attrs map[string]any, want string) {
	t.Helper()
	got, _ := attrs["fake_cli_prior_recall"].(string)
	if got != want {
		t.Fatalf("fake_cli_prior_recall = %q, want %q (attrs: %v)", got, want, attrs)
	}
}

func requireFakeCliResumedWith(t *testing.T, attrs map[string]any, want string) {
	t.Helper()
	got, _ := attrs["fake_cli_resumed_with"].(string)
	if got != want {
		t.Fatalf("fake_cli_resumed_with = %q, want %q (attrs: %v)", got, want, attrs)
	}
}

func requireSessionTokenWritten(t *testing.T, attrs map[string]any, wantRunID string) {
	t.Helper()
	got, _ := attrs["session_token"].(string)
	if got != wantRunID {
		t.Fatalf("session_token = %q, want %q (the dispatch's own runId) (attrs: %v)",
			got, wantRunID, attrs)
	}
}

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
