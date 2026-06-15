// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-lineage-admin end-to-end acceptance proof.
//
// Spec source-of-intent:
//
//	.ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md
//	§STORY-lineage-admin.
//
// Story: "As an operator, I can prune lineage records older than a
// cutoff timestamp, so that the lineage table doesn't grow unbounded
// in a long-lived deployment."
//
// Acceptance: with lineage records of varied ages persisted, an
// operator submits a prune request through the control-api (POST
// /v1/admin/lineage/prune) carrying a cutoff; only records strictly
// older than the cutoff are removed, records at or after the cutoff
// are untouched (verifiable through a follow-up lineage query).
//
// LOAD-BEARING FALSIFIER (the property this proof must pin):
// "Prune removes records at the cutoff boundary, OR removes records
//
//	newer than cutoff, OR silently drops the cutoff and returns a
//	no-op count."
//
// Decisive RED-vs-GREEN discriminators driven through the real
// assembled product (control-api over HTTP, real persistence via
// testcontainers Postgres):
//
//  1. Seed three claim_terminal lineage rows with distinct producer
//     names and distinct observed_at offsets relative to a 24h
//     cutoff: one strictly older (7 days), one at the cutoff boundary
//     (exactly 24h), one strictly newer (1 hour). Each row's run_id
//     + claim_handle_id reference UUIDs that do NOT exist in
//     rimsky_node_runs / rimsky_claim_handles — required so the prune
//     predicate ("observed_at < cutoff AND corresponding run AND
//     claim_handle no longer present") actually fires on the older
//     row. A row referencing a still-live run/claim_handle would not
//     be a candidate for delete and would muddy the falsifier check.
//
//  2. Pre-prune sanity: each producer name surfaces exactly one row
//     via the public read surface `GET /v1/lineage/by-producer/<name>`.
//     Without this baseline the "removed" assertion is vacuous.
//
//  3. POST /v1/admin/lineage/prune with `before` == cutoff (24h ago).
//     The response status is 200 OK and the body reports `deleted: 1`
//     + the `before` echo — exactly the strictly-older row was
//     removed, not the boundary row, not the newer row. The deleted
//     count IS the load-bearing signal that the cutoff was honored
//     (the falsifier "silently drops the cutoff and returns a no-op
//     count" maps to deleted=0 here).
//
//  4. Post-prune verification through the same read surface: the
//     7-day-old producer's row is gone (200 OK + empty records
//     array), the boundary (24h-old) and newer (1h-old) rows are
//     still present. The "at the cutoff boundary" leg is the
//     load-bearing falsifier discriminator — the predicate is
//     `observed_at < cutoff` (strict), not `<=`, so a row whose
//     observed_at equals the cutoff timestamp MUST survive. A
//     cheaper-shape implementation using `<=` would delete the
//     boundary row and red-flag here.
//
// @concept: lineage-record
// @story: lineage-admin
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestLineageAdminPrune drives STORY-lineage-admin's full acceptance
// through the assembled rimsky-all-in-one stack and the public
// control-api routes.
func TestLineageAdminPrune(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	// @constraint: A minimal real template + instance gives the lineage rows a
	// real `instance_id` (rimsky_lineage.instance_id REFERENCES
	// rimsky_instances(id) ON DELETE CASCADE — without a real
	// instance the seed Inserts would fail the FK). The stub
	// executor never has to dispatch — we don't depend on the
	// node settling; we just need a persisted instance row whose
	// id we can stamp on the seed lineage rows. Marking the node
	// type with a non-existent script keeps the runtime from
	// driving anything to terminal during the test window, which
	// the test does not care about.
	h.Stub.WhenType("noop").Success(map[string]any{"ok": true}, true, "ok")
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "lineage-admin-prune-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "noop", Executor: "stub",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-lineage-admin-prune-e2e", map[string]any{})

	// @deliberate: Seed three claim_terminal rows of varied ages whose
	// observed_at offsets are anchored against `now` (the test's clock)
	// so the cutoff math below matches the predicate the route applies.
	// Each row's run_id + claim_handle_id are freshly-minted UUIDs that
	// do NOT exist in rimsky_node_runs / rimsky_claim_handles. The prune
	// predicate is:
	//
	//   observed_at < cutoff
	//     AND NOT EXISTS (rimsky_node_runs with id = record->>'run_id')
	//     AND NOT EXISTS (rimsky_claim_handles with id = record->>'claim_handle_id')
	//
	// So all three rows are prune-CANDIDATES under the orphan
	// predicate; only the age check separates them. This isolates
	// the cutoff cleanly in the falsifier discriminators.
	now := time.Now().UTC()
	const (
		oldProducer      = "prune7d"  // @deliberate: 7 days old — strictly older than 24h → DELETE
		boundaryProducer = "prune24h" // @deliberate: exactly 24h old — at cutoff boundary → KEEP (strict `<`)
		newProducer      = "prune1h"  // @deliberate: 1 hour old — strictly newer than 24h → KEEP
	)
	oldObservedAt := now.Add(-7 * 24 * time.Hour)
	boundaryObservedAt := now.Add(-24 * time.Hour)
	newObservedAt := now.Add(-1 * time.Hour)

	seedClaimTerminal(t, h, iid.String(), oldProducer, oldObservedAt)
	seedClaimTerminal(t, h, iid.String(), boundaryProducer, boundaryObservedAt)
	seedClaimTerminal(t, h, iid.String(), newProducer, newObservedAt)

	require.Equal(t, 1, byProducerCount(t, h, oldProducer),
		"seed sanity: old producer must have exactly 1 row before prune")
	require.Equal(t, 1, byProducerCount(t, h, boundaryProducer),
		"seed sanity: boundary producer must have exactly 1 row before prune")
	require.Equal(t, 1, byProducerCount(t, h, newProducer),
		"seed sanity: new producer must have exactly 1 row before prune")

	// @constraint: The cutoff is the boundary timestamp exactly. The
	// boundary row's observed_at equals the cutoff. Per the route's
	// predicate (`observed_at < cutoff`) it MUST survive — a cheaper
	// shape using `<=` would delete it and the post-prune assertion
	// below would red-flag.
	cutoff := boundaryObservedAt

	pruneBody, err := json.Marshal(map[string]any{
		"before": cutoff.Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	resp, err := http.Post(
		h.ControlBase+"/v1/admin/lineage/prune",
		"application/json",
		bytes.NewReader(pruneBody),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "prune POST status")

	var pruneResp struct {
		Deleted int    `json:"deleted"`
		Before  string `json:"before"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pruneResp))

	// @deliberate: LOAD-BEARING FALSIFIER LEG (count): the deleted count IS the
	// signal that the cutoff was honored. 0 here would prove the
	// "silently drops the cutoff and returns a no-op count" branch
	// of the falsifier. >1 would prove the "removes records at
	// the cutoff boundary OR newer" branch. Exactly 1 is the
	// strict-`<` predicate genuinely firing on the one strictly-older
	// row.
	require.Equal(t, 1, pruneResp.Deleted,
		"prune must delete exactly the strictly-older row (1); count != 1 maps to a falsifier branch")
	require.Equal(t, cutoff.Format(time.RFC3339Nano), pruneResp.Before,
		"prune response must echo the cutoff")

	// @deliberate: Post-prune verification through the public read
	// surface: re-query `GET /v1/lineage/by-producer/<name>` for each
	// producer. The old producer's row must be gone (the prune deleted
	// it); the boundary + new producers' rows must remain.
	require.Equal(t, 0, byProducerCount(t, h, oldProducer),
		"FALSIFIER: the strictly-older row must be removed after prune")
	require.Equal(t, 1, byProducerCount(t, h, boundaryProducer),
		"FALSIFIER: the row AT the cutoff boundary must survive (predicate is strict `<`, not `<=`)")
	require.Equal(t, 1, byProducerCount(t, h, newProducer),
		"FALSIFIER: a row strictly newer than the cutoff must survive")

	t.Logf("STORY-lineage-admin GREEN: deleted=%d (old removed; boundary + new kept)", pruneResp.Deleted)
}

// seedClaimTerminal inserts a single claim_terminal lineage row with
// the given producer_name and observed_at. The row's run_id and
// claim_handle_id are freshly-minted UUIDs that do NOT exist in
// rimsky_node_runs / rimsky_claim_handles, so the row is a candidate
// under the prune's orphan predicate. The producer_name lets the
// public read surface `GET /v1/lineage/by-producer/<name>` find the
// row deterministically without depending on rimsky internals.
func seedClaimTerminal(t *testing.T, h *scenario.Harness, instanceID, producerName string, observedAt time.Time) {
	t.Helper()

	rowID := uuid.New()
	frameID := uuid.New()
	// @deliberate: Synthetic run/claim_handle ids the prune predicate's orphan check
	// can match against (they are not present in rimsky_node_runs /
	// rimsky_claim_handles, so the NOT EXISTS sub-queries are satisfied).
	syntheticRunID := uuid.New().String()
	syntheticClaimHandleID := uuid.New().String()

	record := map[string]any{
		"producer_name":   producerName,
		"version_id":      "v1",
		"run_id":          syntheticRunID,
		"claim_handle_id": syntheticClaimHandleID,
	}
	recordJSON, err := json.Marshal(record)
	require.NoError(t, err)

	iidParsed, err := uuid.Parse(instanceID)
	require.NoError(t, err)

	const insertSQL = `
		INSERT INTO rimsky_lineage (
			id, record_kind, instance_id, frame_id, observed_at, record, outcome
		) VALUES ($1, 'claim_terminal', $2, $3, $4, $5, 'committed')`
	_, err = h.Pool.Exec(h.Ctx, insertSQL, rowID, iidParsed, frameID, observedAt, recordJSON)
	require.NoError(t, err, "seed lineage row for producer %s", producerName)
}

// byProducerCount returns the count of records `GET /v1/lineage/by-producer/<name>`
// returns. Reading through the public route (rather than counting in
// the DB) keeps the assertion bound to the operator-visible surface
// the story names.
func byProducerCount(t *testing.T, h *scenario.Harness, producerName string) int {
	t.Helper()
	url := h.ControlBase + "/v1/lineage/by-producer/" + producerName
	status, body := httpGetJSON(t, url)
	require.Equal(t, http.StatusOK, status, "GET by-producer %s: %s", producerName, body)
	var out struct {
		Records []map[string]any `json:"records"`
	}
	require.NoError(t, json.Unmarshal(body, &out),
		fmt.Sprintf("decode by-producer response for %s: %s", producerName, body))
	return len(out.Records)
}
