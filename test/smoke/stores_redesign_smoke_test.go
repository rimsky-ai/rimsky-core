// Spec §10 — smoke fixture (acceptance criterion for the stores redesign).
//
// Drives the §11.5 four-node template (claim-topic / scope / draft / review)
// against the in-process stack from setup.go: 100 sequential force-fires of
// the claim-topic source node, then poll for the downstream cascade to
// drain.
//
// Wall-clock structure (§10):
//   - Phase 1 (force-fires, sequential): 100 × per-fire wait. Per-fire
//     timeout 5s; happy path is sub-second per fire. Fail-fast on the
//     first per-fire timeout.
//   - Phase 2 (cascade drain, polling): 300s budget for downstream nodes
//     (scope, draft, review) to drain.
//
// Final assertions: 100 work_completed events for review, dispatch queue
// empty, lock holders empty, no active claim holders, all 100 items
// returned to topics_items.state='available', /health returns 200.
//
// Run with `go test ./test/smoke/... -count=1 -timeout 10m`.
package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestStoresRedesignSmoke is the §10 acceptance test. The whole flow must
// complete inside the 10-minute global budget; the per-fire and cascade
// budgets are enforced internally so a hang in either phase fails fast
// rather than waiting for the global timeout.
func TestStoresRedesignSmoke(t *testing.T) {
	stack := BringUpStack(t)
	ctx := context.Background()

	// ---- Bulk-insert 100 items ----
	bulkInsertItems(t, stack, 100)

	// ---- Deploy template + create one instance ----
	templateID := deploySmokeTemplate(t, stack)
	instanceID := createSmokeInstance(t, stack, templateID)

	// ---- Find the claim-topic node ID for this instance ----
	claimTopicID := findNodeIDByType(t, stack, instanceID, "claim-topic")

	// ---- Phase 1: 100 sequential force-fires ----
	const phase1PerFireTimeout = 5 * time.Second
	const phase1PollInterval = 50 * time.Millisecond

	startPhase1 := time.Now()
	for n := 1; n <= 100; n++ {
		fireOnceAndWait(t, stack, claimTopicID, n, phase1PerFireTimeout, phase1PollInterval)
	}
	t.Logf("phase 1 complete: 100 force-fires in %v", time.Since(startPhase1))

	// ---- Phase 2: poll for downstream-cascade steady-state ----
	const phase2Timeout = 300 * time.Second
	const phase2PollInterval = 250 * time.Millisecond

	startPhase2 := time.Now()
	deadline := time.Now().Add(phase2Timeout)
	for time.Now().Before(deadline) {
		if cascadeAtSteadyState(t, ctx, stack) {
			break
		}
		time.Sleep(phase2PollInterval)
	}
	if time.Now().After(deadline) {
		dumpDiagnostics(t, ctx, stack)
		t.Fatalf("downstream cascade did not reach steady state within %v", phase2Timeout)
	}
	t.Logf("phase 2 complete: cascade drained in %v", time.Since(startPhase2))

	// ---- Final assertions per §10 ----
	assertFinalState(t, ctx, stack)
}

// bulkInsertItems POSTs 100 items via the store-internal admin
// endpoint of the postgres store-service (per v3 spec §7.3 step 1: rimsky no
// longer has an items endpoint; the store-service owns its own admin
// surface).
func bulkInsertItems(t *testing.T, stack *SmokeStack, count int) {
	t.Helper()
	items := make([]map[string]any, 0, count)
	for n := 1; n <= count; n++ {
		items = append(items, map[string]any{
			"payload": map[string]any{
				"area":     fmt.Sprintf("A_%d", n),
				"subtopic": fmt.Sprintf("S_%d", n),
			},
		})
	}
	status, raw := stack.PostStoreAdmin("/admin/items/@review-queue", map[string]any{"items": items})
	require.Equal(t, http.StatusCreated, status, "bulkInsertItems: %s", string(raw))
	var resp struct {
		Inserted int `json:"inserted"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.Equal(t, count, resp.Inserted)
}

// deploySmokeTemplate POSTs the §11.5 four-node template to /templates,
// then transitions to deployed state, returning the new template hash.
// The `model-budget` lock limit is set to 50 per §10 to keep executor
// parallelism unconstrained.
//
// The §11.5 example carries a `quality_rules` entry with type
// `must_match_regex`. v1 only ships `row_count_ratio` / `no_nulls` /
// `nullable_fields_present` (see core/qualityrule/rules.go); the smoke
// fixture omits the rule from the deployed template body so the
// supervisor's per-commit evaluator does not error on an unregistered
// type. The fixtures/template.yml file documents the spec'd rule alongside.
func deploySmokeTemplate(t *testing.T, stack *SmokeStack) string {
	t.Helper()
	status, raw := stack.PostJSON("/templates", smokeTemplateBody())
	require.Equal(t, http.StatusCreated, status, "deploySmokeTemplate: %s", string(raw))
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	hash := resp.TemplateID
	require.NotEmpty(t, hash)
	// Transition the freshly-registered template to 'deployed' so the
	// /instances handler will accept it (per spec §1.4 / §2.2).
	deployStatus, deployRaw := stack.PostJSON("/templates/"+hash+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, "deploySmokeTemplate: deploy: %s", string(deployRaw))
	return hash
}

// createSmokeInstance POSTs to /instances with instance_key="smoke-1"
// and returns the new instance_id.
func createSmokeInstance(t *testing.T, stack *SmokeStack, templateHash string) uuid.UUID {
	t.Helper()
	instanceKey := "smoke-1"
	status, raw := stack.PostJSON("/instances", map[string]any{
		"template":     templateHash,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	require.Equal(t, http.StatusCreated, status, "createSmokeInstance: %s", string(raw))
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	return stack.AssertUUID(resp.InstanceID)
}

// findNodeIDByType looks up a node by (instance_id, node_type) via direct
// SQL. The control-api currently has no by-type query; SQL is the
// shortest path.
func findNodeIDByType(t *testing.T, stack *SmokeStack, instanceID uuid.UUID, nodeType string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := stack.Pool.QueryRow(context.Background(),
		`SELECT id FROM rimsky_nodes WHERE instance_id = $1 AND node_type = $2`,
		instanceID, nodeType,
	).Scan(&id)
	require.NoError(t, err, "findNodeIDByType: %s", nodeType)
	return id
}

// fireOnceAndWait POSTs a force-fire and polls the source node row until
// it (a) leaves its current state and (b) returns to fresh. Per spec
// §10 we want one full cycle per force-fire (no coalescing) — the
// `updated_at` snapshot before the POST guarantees we count only the new
// cycle's transitions. Times out after `timeout` and fails the test
// (fail-fast: a single per-fire timeout terminates Phase 1).
func fireOnceAndWait(
	t *testing.T,
	stack *SmokeStack,
	nodeID uuid.UUID,
	fireN int,
	timeout time.Duration,
	pollInterval time.Duration,
) {
	t.Helper()
	ctx := context.Background()

	// Snapshot the current updated_at so we can detect a NEW transition
	// to fresh after the force-fire (not the prior fresh state).
	var beforeUpdatedAt time.Time
	err := stack.Pool.QueryRow(ctx,
		`SELECT updated_at FROM rimsky_nodes WHERE id = $1`, nodeID,
	).Scan(&beforeUpdatedAt)
	require.NoError(t, err, "fire %d: read updated_at", fireN)

	status, raw := stack.PostJSON(fmt.Sprintf("/admin/scheduled-nodes/%s/force-fire", nodeID), nil)
	require.Equal(t, http.StatusNoContent, status, "fire %d: force-fire: %s", fireN, string(raw))

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var state string
		var updatedAt time.Time
		err := stack.Pool.QueryRow(ctx,
			`SELECT state, updated_at FROM rimsky_nodes WHERE id = $1`, nodeID,
		).Scan(&state, &updatedAt)
		if err == nil && state == "fresh" && updatedAt.After(beforeUpdatedAt) {
			return
		}
		if err == nil && state == "failed" {
			t.Fatalf("fire %d: source node entered failed state", fireN)
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("fire %d: source node did not return to fresh within %v (last seen pre-fire updated_at=%v)",
		fireN, timeout, beforeUpdatedAt)
}

// cascadeAtSteadyState reports whether the four §10 steady-state checks
// are simultaneously true:
//
//  1. count(rimsky_events kind='work_completed' payload->>'node_type'='review') >= 100
//  2. count(rimsky_worker_request claimed_by IS NOT NULL) = 0
//  3. count(rimsky_claim_handle) = 0
//  4. count(rimsky_claim_holders state='active') = 0
func cascadeAtSteadyState(t *testing.T, ctx context.Context, stack *SmokeStack) bool {
	t.Helper()
	var reviewCount int
	if err := stack.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_events
		   WHERE kind = 'work_completed' AND payload->>'node_type' = 'review'`,
	).Scan(&reviewCount); err != nil {
		t.Fatalf("cascadeAtSteadyState: review count: %v", err)
	}
	if reviewCount < 100 {
		return false
	}

	var inflightDispatch int
	if err := stack.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_worker_request WHERE claimed_by IS NOT NULL`,
	).Scan(&inflightDispatch); err != nil {
		t.Fatalf("cascadeAtSteadyState: in-flight dispatch: %v", err)
	}
	if inflightDispatch != 0 {
		return false
	}

	var lockHolders int
	if err := stack.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handle`,
	).Scan(&lockHolders); err != nil {
		t.Fatalf("cascadeAtSteadyState: lock holders: %v", err)
	}
	if lockHolders != 0 {
		return false
	}

	var activeClaimHolders int
	if err := stack.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_holders WHERE state = 'active'`,
	).Scan(&activeClaimHolders); err != nil {
		t.Fatalf("cascadeAtSteadyState: active claim holders: %v", err)
	}
	return activeClaimHolders == 0
}

// dumpDiagnostics emits a compact view of pending dispatch / nodes /
// events / claim-holders so a Phase-2 timeout doesn't require a manual
// `psql` walk to see what's stuck. Logs at t.Logf so failure output
// shows it; never fails the test (always called from a path that's
// about to fail anyway).
func dumpDiagnostics(t *testing.T, ctx context.Context, stack *SmokeStack) {
	t.Helper()
	rows, err := stack.Pool.Query(ctx,
		`SELECT n.node_type, n.state, count(*) FROM rimsky_nodes n GROUP BY 1, 2 ORDER BY 1, 2`)
	if err == nil {
		t.Logf("rimsky_nodes counts:")
		for rows.Next() {
			var typ, st string
			var c int
			_ = rows.Scan(&typ, &st, &c)
			t.Logf("  %s/%s: %d", typ, st, c)
		}
		rows.Close()
	}

	rows, err = stack.Pool.Query(ctx,
		`SELECT count(*) FILTER (WHERE claimed_by IS NULL),
		        count(*) FILTER (WHERE claimed_by IS NOT NULL)
		   FROM rimsky_worker_request`)
	if err == nil && rows.Next() {
		var unclaimed, claimed int
		_ = rows.Scan(&unclaimed, &claimed)
		t.Logf("rimsky_worker_request: unclaimed=%d claimed=%d", unclaimed, claimed)
		rows.Close()
	}

	rows, err = stack.Pool.Query(ctx,
		`SELECT count(*) FROM rimsky_claim_handle`)
	if err == nil && rows.Next() {
		var n int
		_ = rows.Scan(&n)
		t.Logf("rimsky_claim_handle: %d", n)
		rows.Close()
	}

	rows, err = stack.Pool.Query(ctx,
		`SELECT state, count(*) FROM rimsky_claim_holders GROUP BY 1 ORDER BY 1`)
	if err == nil {
		t.Logf("rimsky_claim_holders by state:")
		for rows.Next() {
			var st string
			var c int
			_ = rows.Scan(&st, &c)
			t.Logf("  %s: %d", st, c)
		}
		rows.Close()
	}

	rows, err = stack.Pool.Query(ctx,
		`SELECT payload->>'node_type' AS nt, count(*) FROM rimsky_events
		   WHERE kind = 'work_completed' GROUP BY 1 ORDER BY 1`)
	if err == nil {
		t.Logf("work_completed by node_type:")
		for rows.Next() {
			var nt string
			var c int
			_ = rows.Scan(&nt, &c)
			t.Logf("  %s: %d", nt, c)
		}
		rows.Close()
	}

	rows, err = stack.Pool.Query(ctx,
		`SELECT kind, count(*) FROM rimsky_events GROUP BY 1 ORDER BY count(*) DESC LIMIT 20`)
	if err == nil {
		t.Logf("event kinds (top 20):")
		for rows.Next() {
			var kind string
			var c int
			_ = rows.Scan(&kind, &c)
			t.Logf("  %s: %d", kind, c)
		}
		rows.Close()
	}

	// Show recent error events with their payload, since that's what
	// usually explains "node stuck"
	rows, err = stack.Pool.Query(ctx,
		`SELECT node_id, kind, payload FROM rimsky_events
		  WHERE kind IN ('error','quality_rule_failed','attributes_schema_failed','unresolved_executor','orphaned_claim_lost_race','lock_orphan_reaped','schedule_dispatch_failed')
		  ORDER BY occurred_at DESC LIMIT 10`)
	if err == nil {
		t.Logf("recent error events:")
		for rows.Next() {
			var nid uuid.UUID
			var kind string
			var raw []byte
			_ = rows.Scan(&nid, &kind, &raw)
			t.Logf("  node=%s kind=%s payload=%s", nid, kind, string(raw))
		}
		rows.Close()
	}
}

// assertFinalState runs the post-cascade SQL assertions called out in
// §10: every item back to 'available' (none in_progress / dead_letter),
// the items table contains exactly 100 rows (ring buffer never deletes),
// and the control-api /health endpoint is reachable.
//
// On failure of the not-available check we dump the offending items
// table rows + their associated rimsky_claim_holders rows + dispatch
// history so a regression can be diagnosed without re-running with
// hand-rolled psql instrumentation. The dump is harmless on success
// (gated by the failed-count check) and stays in place per Issue A's
// fix-instructions.
func assertFinalState(t *testing.T, ctx context.Context, stack *SmokeStack) {
	t.Helper()

	var notAvailable int
	require.NoError(t, stack.Pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE state != 'available'`, stack.ItemsTable),
	).Scan(&notAvailable))
	if notAvailable != 0 {
		dumpStuckItemsDiagnostics(t, ctx, stack)
	}
	require.Equal(t, 0, notAvailable, "all items should be released back to available")

	var available int
	require.NoError(t, stack.Pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE state = 'available'`, stack.ItemsTable),
	).Scan(&available))
	require.Equal(t, 100, available, "ring buffer must never delete items")

	resp, err := http.Get(stack.ControlBase + "/health")
	require.NoError(t, err, "GET /health")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "/health should return 200")
}

// dumpStuckItemsDiagnostics emits, for every items-table row not in
// state='available', (a) the items-table row itself, (b) every
// rimsky_claim_holders row keyed by that item_id, and (c) any current
// dispatch row keyed off the same claim. Designed to make the
// "two items stuck in_progress while all holders show completed"
// failure mode self-explanatory in test output.
//
// Always called from a path that's about to fail an assertion, so we
// don't propagate query errors — best effort.
func dumpStuckItemsDiagnostics(t *testing.T, ctx context.Context, stack *SmokeStack) {
	t.Helper()

	stuckQ := fmt.Sprintf(
		`SELECT item_id, state, claim_token, claimed_at FROM %s
		   WHERE state != 'available' ORDER BY item_id`,
		stack.ItemsTable,
	)
	rows, err := stack.Pool.Query(ctx, stuckQ)
	if err != nil {
		t.Logf("dumpStuckItemsDiagnostics: query stuck items: %v", err)
		return
	}
	type stuckItem struct {
		ItemID     string
		State      string
		ClaimToken *string
		ClaimedAt  *time.Time
	}
	var stuck []stuckItem
	for rows.Next() {
		var s stuckItem
		_ = rows.Scan(&s.ItemID, &s.State, &s.ClaimToken, &s.ClaimedAt)
		stuck = append(stuck, s)
	}
	rows.Close()

	t.Logf("=== stuck items diagnostics: %d rows not in 'available' ===", len(stuck))
	for _, s := range stuck {
		ct := "<nil>"
		if s.ClaimToken != nil {
			ct = *s.ClaimToken
		}
		ca := "<nil>"
		if s.ClaimedAt != nil {
			ca = s.ClaimedAt.Format(time.RFC3339Nano)
		}
		t.Logf("item_id=%s state=%s claim_token=%s claimed_at=%s",
			s.ItemID, s.State, ct, ca)

		// Per-item: every rimsky_claim_holders row whose lock-holder
		// row points at this item via scope_data. Under v2 the
		// claim-holders rows key on claim_handle_id (FK to
		// rimsky_claim_handle); we join both so the dump shows the
		// full ledger for items still held by some node's claim.
		hrows, herr := stack.Pool.Query(ctx,
			`SELECT ch.id, ch.claim_handle_id, ch.holder_node_id,
			        ch.state, ch.completed_at,
			        lh.store_name, lh.scope_data
			   FROM rimsky_claim_holders ch
			   JOIN rimsky_claim_handle lh ON lh.id = ch.claim_handle_id
			  WHERE lh.scope_data::text = $1
			  ORDER BY ch.id`,
			fmt.Sprintf("%q", s.ItemID),
		)
		if herr != nil {
			t.Logf("  claim_holders query: %v", herr)
			continue
		}
		anyHolders := false
		for hrows.Next() {
			anyHolders = true
			var (
				hid          uuid.UUID
				lockHolderID uuid.UUID
				holderNodeID uuid.UUID
				state        string
				completedAt  *time.Time
				storeName    string
				scopeData    []byte
			)
			_ = hrows.Scan(&hid, &lockHolderID, &holderNodeID,
				&state, &completedAt, &storeName, &scopeData)
			cAt := "<nil>"
			if completedAt != nil {
				cAt = completedAt.Format(time.RFC3339Nano)
			}
			t.Logf("  claim_holder id=%s lock_holder=%s store=%s holder_node=%s state=%s completed_at=%s scope=%s",
				hid, lockHolderID, storeName, holderNodeID, state, cAt, string(scopeData))
		}
		hrows.Close()
		if !anyHolders {
			t.Logf("  (no rimsky_claim_holders rows for this item_id)")
		}

		// Per-item: any in-flight dispatch rows that may still reference
		// the holder nodes for this claim.
		drows, derr := stack.Pool.Query(ctx,
			`SELECT d.id, d.node_id, d.claimed_by, d.enqueued_at, d.frame_id
			   FROM rimsky_worker_request d
			   JOIN rimsky_claim_holders ch ON ch.holder_node_id = d.node_id
			   JOIN rimsky_claim_handle lh ON lh.id = ch.claim_handle_id
			  WHERE lh.scope_data::text = $1`,
			fmt.Sprintf("%q", s.ItemID),
		)
		if derr != nil {
			t.Logf("  dispatch query: %v", derr)
			continue
		}
		anyDispatch := false
		for drows.Next() {
			anyDispatch = true
			var (
				did        uuid.UUID
				nodeID     uuid.UUID
				claimedBy  *string
				enqueuedAt time.Time
				frameID    *uuid.UUID
			)
			_ = drows.Scan(&did, &nodeID, &claimedBy, &enqueuedAt, &frameID)
			cb := "<nil>"
			if claimedBy != nil {
				cb = *claimedBy
			}
			fid := "<nil>"
			if frameID != nil {
				fid = frameID.String()
			}
			t.Logf("  dispatch id=%s node=%s claimed_by=%s enqueued_at=%s frame_id=%s",
				did, nodeID, cb, enqueuedAt.Format(time.RFC3339Nano), fid)
		}
		drows.Close()
		if !anyDispatch {
			t.Logf("  (no rimsky_worker_request rows referencing this claim's holders)")
		}
	}

	// Aggregate counts to provide a population view alongside the
	// per-stuck-item walk.
	var (
		totalHolders     int
		activeHolders    int
		completedHolders int
	)
	_ = stack.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE TRUE),
		        count(*) FILTER (WHERE state = 'active'),
		        count(*) FILTER (WHERE state = 'completed')
		   FROM rimsky_claim_holders`,
	).Scan(&totalHolders, &activeHolders, &completedHolders)
	t.Logf("rimsky_claim_holders aggregate: total=%d active=%d completed=%d",
		totalHolders, activeHolders, completedHolders)

}

// smokeTemplateBody returns the JSON-shaped POST /templates body for the
// §11.5 four-node template. The structure is built in Go (rather than
// loading from `fixtures/template.yml`) to keep the wire shape readable
// and to allow the omitted-quality-rule note (see deploySmokeTemplate
// docstring) to be expressed in code; the YAML fixture remains as
// documentation alongside.
//
// The model-budget lock limit is 50 (§10 requirement).
func smokeTemplateBody() map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":             "smoke-stores-redesign",
			"version":          "1",
			"frame_resolution": "serial_queue",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				claimTopicNode(),
				scopeNode(),
				draftNode(),
				reviewNode(),
			},
		},
	}
}

// claimTopicNode is the source: cron-scheduled, holds a pick-policy
// claim against `topics-ring` selected via the `@review-queue` selector.
// No executor (native claim-only path); per-claim resolution declared
// on this node since it is the acquirer of the held subgraph and
// downstream `review` inherits the claim.
func claimTopicNode() map[string]any {
	return map[string]any{
		"type":     "claim-topic",
		"schedule": "* * * * *",
		"stores": []map[string]any{
			{"name": "topics-ring", "selector": "@review-queue", "intent": "rw"},
		},
		"locks": []map[string]any{
			{"name": "topics-ring:concurrent-claims"},
		},
		"attributes": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"area":     map[string]any{"type": "string", "source": "{{claim.topics-ring.payload.area}}"},
					"subtopic": map[string]any{"type": "string", "source": "{{claim.topics-ring.payload.subtopic}}"},
				},
				"required": []any{"area", "subtopic"},
			},
		},
	}
}

// scopeNode depends on claim-topic; pulls area/subtopic from deps and
// receives `scope_notes` from the executor (stub returns "stub"). Holds
// the model-budget counting lock and inherits the topics-ring claim
// from claim-topic for value-passing only.
func scopeNode() map[string]any {
	return map[string]any{
		"type":         "scope",
		"dependencies": []string{"claim-topic"},
		"executor":     "claude-agent",
		"attributes": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"area":        map[string]any{"type": "string", "source": "{{deps.claim-topic.area}}"},
					"subtopic":    map[string]any{"type": "string", "source": "{{deps.claim-topic.subtopic}}"},
					"scope_notes": map[string]any{"type": "string"},
				},
				"required": []any{"scope_notes"},
			},
		},
		"userdata": map[string]any{
			"model":             "claude-sonnet-4-6",
			"system_prompt_ref": "scope-system.md",
		},
		"locks": []map[string]any{
			{"name": "model-budget"},
		},
		"error_types": map[string]any{
			"review_rejected": map[string]any{
				"policy": []map[string]any{
					{"action": "discard_then_retry", "count": 2},
					{"action": "invalidate", "targets": []string{"scope"}},
					{"action": "give_up"},
				},
			},
		},
	}
}

// draftNode depends on claim-topic + scope; declares a write scope into
// the `content` filesystem store substituted from upstream attributes.
// Stub returns an empty attributes_delta.
//
// `quality_rules` from §11.5 (must_match_regex) is intentionally omitted —
// the rule type isn't registered in v1 (see core/qualityrule/rules.go);
// the YAML fixture file documents what the spec example carries.
func draftNode() map[string]any {
	return map[string]any{
		"type":         "draft",
		"dependencies": []string{"claim-topic", "scope"},
		"executor":     "claude-agent",
		"attributes": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"area":        map[string]any{"type": "string", "source": "{{deps.claim-topic.area}}"},
					"subtopic":    map[string]any{"type": "string", "source": "{{deps.claim-topic.subtopic}}"},
					"scope_notes": map[string]any{"type": "string", "source": "{{deps.scope.scope_notes}}"},
				},
				"required": []any{"area", "subtopic", "scope_notes"},
			},
		},
		"stores": []map[string]any{
			{
				"name":     "content",
				"selector": "items/{{deps.claim-topic.area}}/{{deps.claim-topic.subtopic}}.md",
				"intent":   "rw",
			},
		},
		"userdata": map[string]any{
			"model":             "claude-sonnet-4-6",
			"system_prompt_ref": "draft-system.md",
		},
		"locks": []map[string]any{
			{"name": "model-budget"},
		},
	}
}

// reviewNode is the terminal of the held subgraph: it inherits the
// topics-ring claim from claim-topic (value-passing through scope is
// not transitive — each downstream that needs the live claim declares
// it explicitly). At terminal the supervisor fires Commit on the
// store (success) or Abandon (failure); the postgres reference
// store-service's per-policy `on_commit_default` /
// `on_give_up_default` config governs disposition. This node just
// reads the draft's output scope.
func reviewNode() map[string]any {
	return map[string]any{
		"type":         "review",
		"dependencies": []string{"claim-topic", "scope", "draft"},
		"executor":     "claude-agent",
		"attributes": map[string]any{
			"schema": map[string]any{
				"properties": map[string]any{
					"accepted": map[string]any{"type": "boolean"},
					"notes":    map[string]any{"type": "string"},
				},
				"required": []any{"accepted"},
			},
		},
		"stores": []map[string]any{
			{
				"name":     "content",
				"selector": "items/{{deps.claim-topic.area}}/{{deps.claim-topic.subtopic}}.md",
				"intent":   "r",
			},
		},
		"inherits": []map[string]any{
			{"claim": "topics-ring"},
		},
		"userdata": map[string]any{
			"model":             "claude-sonnet-4-6",
			"system_prompt_ref": "review-system.md",
		},
		"locks": []map[string]any{
			{"name": "model-budget"},
		},
	}
}
