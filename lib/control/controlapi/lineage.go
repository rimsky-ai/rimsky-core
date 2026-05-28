// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lineage.go — F6. Lineage query surface.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Content lineage / Query surface.
//
//   - GET /lineage/runs/{run_id}
//   - GET /lineage/runs/{run_id}/ancestors?depth=N
//   - GET /lineage/runs/{run_id}/descendants?depth=N
//   - GET /lineage/claims/{claim_handle_id}
//   - GET /lineage/claims/{claim_handle_id}/ancestors?depth=N
//   - GET /lineage/by-source/{source_type}/{source_id}
//   - GET /lineage/by-producer/{executor_name}?version=...
//
// @concept: lineage-record
//
// The lineage projection is rebuildable from `rimsky_events` +
// `rimsky_claim_handles`; this surface is the read-side projection over
// `table:rimsky_lineage`.

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	// lineageWalkDefaultDepth is the depth used when the caller omits
	// the `?depth=` query param. Modest by design; deep walks are
	// expensive and operator-friendly defaults beat surprise timeouts.
	lineageWalkDefaultDepth = 3
	// lineageWalkMaxDepth caps the depth a single request can request.
	// Per spec §Content lineage / Query surface, walks bounded at 50.
	lineageWalkMaxDepth = 50
	// lineageWalkPerFrontierLimit caps the per-frontier-id descendant
	// query so a single hot frontier id can't blow the projection
	// scan. 1000 covers realistic fan-out widths; truly oversized
	// fan-outs should use the OpenLineage emitter (canonical bulk
	// walker) instead of this operator-facing endpoint.
	lineageWalkPerFrontierLimit = 1000
)

func registerLineageRoutes(r chi.Router, deps AppDeps) {
	r.Get("/lineage/runs/{run_id}", gate(deps, "lineage:read", handleLineageRun(deps)))
	r.Get("/lineage/runs/{run_id}/ancestors", gate(deps, "lineage:read", handleLineageRunAncestors(deps)))
	r.Get("/lineage/runs/{run_id}/descendants", gate(deps, "lineage:read", handleLineageRunDescendants(deps)))
	r.Get("/lineage/claims/{claim_handle_id}", gate(deps, "lineage:read", handleLineageClaim(deps)))
	r.Get("/lineage/claims/{claim_handle_id}/ancestors", gate(deps, "lineage:read", handleLineageClaimAncestors(deps)))
	r.Get("/lineage/by-source/{source_type}/{source_id}", gate(deps, "lineage:read", handleLineageBySource(deps)))
	r.Get("/lineage/by-producer/{executor_name}", gate(deps, "lineage:read", handleLineageByProducer(deps)))
	r.Post("/admin/lineage/prune", gate(deps, "lineage:prune", handleLineagePrune(deps)))
}

// pruneLineageRequest is the body of POST /admin/lineage/prune.
type pruneLineageRequest struct {
	// Before is an RFC3339 timestamp. Records with observed_at strictly
	// older than this are deleted.
	Before string `json:"before"`
}

// handleLineagePrune deletes lineage rows older than `before`. Wraps
// `code:foundation/persistence/lineage.go::LineageTable.DeleteOlderThan`
// so operators can prune the projection from the CLI (G4) without
// reaching for SQL. Returns `{deleted: N, before: <timestamp>}` on
// success.
func handleLineagePrune(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body pruneLineageRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if body.Before == "" {
			badRequest(w, "before is required (RFC3339 timestamp)")
			return
		}
		cutoff, err := time.Parse(time.RFC3339, body.Before)
		if err != nil {
			badRequest(w, "before must be RFC3339 timestamp: "+err.Error())
			return
		}
		if WriteDryRunResponse(w, req, "would_have_pruned", map[string]any{
			"before": body.Before,
		}) {
			return
		}
		n, err := deps.Persist.Lineage().DeleteOlderThan(req.Context(), cutoff)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted": n,
			"before":  body.Before,
		})
	}
}

// lineageRecordItem is the JSON projection of persistence.LineageRow.
// `record` is forwarded as-is — the per-kind shape is documented in
// spec §Content lineage and the consumer (CLI, OpenLineage emitter)
// decodes it.
type lineageRecordItem struct {
	ID         string          `json:"id"`
	RecordKind string          `json:"record_kind"`
	InstanceID string          `json:"instance_id"`
	FrameID    string          `json:"frame_id"`
	ObservedAt time.Time       `json:"observed_at"`
	Record     json.RawMessage `json:"record"`
}

func toLineageItem(r persistence.LineageRow) lineageRecordItem {
	return lineageRecordItem{
		ID:         r.ID.String(),
		RecordKind: r.RecordKind,
		InstanceID: r.InstanceID.String(),
		FrameID:    r.FrameID.String(),
		ObservedAt: r.ObservedAt,
		Record:     r.Record,
	}
}

// parseDepth returns the requested walk depth, defaulted and capped.
func parseDepth(req *http.Request) int {
	s := req.URL.Query().Get("depth")
	if s == "" {
		return lineageWalkDefaultDepth
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return lineageWalkDefaultDepth
	}
	if n > lineageWalkMaxDepth {
		return lineageWalkMaxDepth
	}
	return n
}

// handleLineageRun returns the most recent leaf_run record for run_id.
// Spec §Query surface / GET /lineage/runs/{run_id}.
func handleLineageRun(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		runID, err := uuid.Parse(chi.URLParam(req, "run_id"))
		if err != nil {
			badRequest(w, "invalid run_id")
			return
		}
		rows, err := deps.Persist.Lineage().GetByRunID(req.Context(), shared.UUID(runID))
		if err != nil {
			writeError(w, err)
			return
		}
		if len(rows) == 0 {
			notFoundResp(w, "lineage record not found")
			return
		}
		// GetByRunID is observed_at ASC; the caller wants the most
		// recent terminal record for the run.
		writeJSON(w, http.StatusOK, toLineageItem(rows[len(rows)-1]))
	}
}

// handleLineageRunAncestors walks backward from `run_id` resolving via
// substitution refs and held-claim writers. For V1 the walk is
// approximated by listing all leaf_run rows whose `record->>'run_id'`
// matches refs in the seed run — bounded by `depth`. Full graph
// traversal is the OpenLineage subscriber's domain; this endpoint is a
// thin operator surface.
func handleLineageRunAncestors(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		runID, err := uuid.Parse(chi.URLParam(req, "run_id"))
		if err != nil {
			badRequest(w, "invalid run_id")
			return
		}
		depth := parseDepth(req)
		ancestors, err := walkLineageRuns(req.Context(), deps, shared.UUID(runID), depth, lineageWalkDirectionAncestors)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]lineageRecordItem, 0, len(ancestors))
		for _, lr := range ancestors {
			items = append(items, toLineageItem(lr))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ancestors": items,
			"depth":     depth,
		})
	}
}

func handleLineageRunDescendants(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		runID, err := uuid.Parse(chi.URLParam(req, "run_id"))
		if err != nil {
			badRequest(w, "invalid run_id")
			return
		}
		depth := parseDepth(req)
		descendants, err := walkLineageRuns(req.Context(), deps, shared.UUID(runID), depth, lineageWalkDirectionDescendants)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]lineageRecordItem, 0, len(descendants))
		for _, lr := range descendants {
			items = append(items, toLineageItem(lr))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"descendants": items,
			"depth":       depth,
		})
	}
}

// lineageWalkDirection selects the BFS walk direction.
type lineageWalkDirection int

const (
	lineageWalkDirectionAncestors lineageWalkDirection = iota
	lineageWalkDirectionDescendants
)

// walkLineageRuns performs a BFS over leaf_run records up to `depth`,
// resolving links via the leaf_run record's `substitution_refs` field
// for ancestor walks and via the `parent_run_id` JSONB path for
// descendant walks.
//
// Both directions emit ONLY relatives of the seed — never the seed's
// own row. `GET /lineage/runs/{run_id}` is the surface for "the run
// itself"; `/ancestors` and `/descendants` return strictly the
// neighborhood.
//
// Ancestor direction: for each frontier id, look up the row, extract
// its `substitution_refs` (upstream run ids), and emit the ancestor's
// own lineage row plus enqueue the ancestor id for the next BFS level.
//
// Descendant direction: for each frontier id, query lineage rows whose
// `record->>'parent_run_id'` matches that id directly. The query is
// implemented as a JSONB key lookup (postgres) / `json_extract` filter
// (sqlite) on `persistence.LineageTable.QueryByParentRunID`, which
// scales with the fan-out under each frontier id rather than the size
// of the entire per-instance projection. The pre-2026-05-17 code
// paged the per-instance projection at LIMIT 200 and filtered in Go,
// silently truncating deeper trees.
func walkLineageRuns(
	ctx context.Context, deps AppDeps,
	seed shared.UUID, depth int, dir lineageWalkDirection,
) ([]persistence.LineageRow, error) {
	visited := map[shared.UUID]struct{}{seed: {}}
	var out []persistence.LineageRow
	frontier := []shared.UUID{seed}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		next := []shared.UUID{}
		for _, id := range frontier {
			switch dir {
			case lineageWalkDirectionAncestors:
				// Ancestor direction emits ONLY upstream runs, never the
				// frontier id's own row. The seed must not appear in its
				// own ancestors set; each ancestor shows up exactly once
				// (the descendant that pulled it in via substitution_refs).
				frontierRecords, err := deps.Persist.Lineage().GetByRunID(ctx, id)
				if err != nil {
					return nil, err
				}
				for _, fr := range frontierRecords {
					for _, refID := range extractSubstitutionRefRunIDs(fr.Record) {
						if _, seen := visited[refID]; seen {
							continue
						}
						visited[refID] = struct{}{}
						ancestorRows, err := deps.Persist.Lineage().GetByRunID(ctx, refID)
						if err != nil {
							return nil, err
						}
						// Append the most recent terminal record for the
						// ancestor run; GetByRunID is observed_at ASC.
						if len(ancestorRows) > 0 {
							out = append(out, ancestorRows[len(ancestorRows)-1])
						}
						next = append(next, refID)
					}
				}
			case lineageWalkDirectionDescendants:
				// Descendant direction emits ONLY children, never the
				// frontier id's own row. The seed must not appear in its
				// own descendants set; each child shows up exactly once
				// (the parent that pulled it in).
				children, err := deps.Persist.Lineage().QueryByParentRunID(ctx, id, lineageWalkPerFrontierLimit)
				if err != nil {
					return nil, err
				}
				for _, r := range children {
					childRunID := extractRunIDFromRecord(r.Record)
					if childRunID == (shared.UUID{}) {
						continue
					}
					if _, seen := visited[childRunID]; seen {
						continue
					}
					visited[childRunID] = struct{}{}
					next = append(next, childRunID)
					out = append(out, r)
				}
			}
		}
		frontier = next
	}
	return out, nil
}

// extractSubstitutionRefRunIDs reads the `substitution_refs` slice of
// a leaf_run record and returns the upstream run ids referenced. The
// walker matches by `source_version_or_id` (per spec §F6 ancestors
// step 2). Only entries whose `source_version_or_id` is a parseable
// UUID participate in the run-ancestor walk; directive-shape entries
// (kind=`attribute` / `event`, whose version_or_id is the attribute /
// event name) are silently skipped — they're informational rather
// than lineage-link material.
func extractSubstitutionRefRunIDs(record json.RawMessage) []shared.UUID {
	if len(record) == 0 {
		return nil
	}
	var rec struct {
		SubstitutionRefs []struct {
			SourceKind        string `json:"source_kind"`
			SourceVersionOrID string `json:"source_version_or_id"`
		} `json:"substitution_refs"`
	}
	if err := json.Unmarshal(record, &rec); err != nil {
		return nil
	}
	out := make([]shared.UUID, 0, len(rec.SubstitutionRefs))
	for _, r := range rec.SubstitutionRefs {
		if u, err := uuid.Parse(r.SourceVersionOrID); err == nil {
			out = append(out, shared.UUID(u))
		}
	}
	return out
}

func extractRunIDFromRecord(record json.RawMessage) shared.UUID {
	if len(record) == 0 {
		return shared.UUID{}
	}
	var rec struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(record, &rec); err != nil {
		return shared.UUID{}
	}
	if u, err := uuid.Parse(rec.RunID); err == nil {
		return shared.UUID(u)
	}
	return shared.UUID{}
}

// handleLineageClaim returns the most recent claim_terminal record for
// the given claim_handle_id.
func handleLineageClaim(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		claimID, err := uuid.Parse(chi.URLParam(req, "claim_handle_id"))
		if err != nil {
			badRequest(w, "invalid claim_handle_id")
			return
		}
		rows, err := deps.Persist.Lineage().GetByClaimHandleID(req.Context(), shared.UUID(claimID))
		if err != nil {
			writeError(w, err)
			return
		}
		if len(rows) == 0 {
			notFoundResp(w, "claim_terminal lineage record not found")
			return
		}
		writeJSON(w, http.StatusOK, toLineageItem(rows[len(rows)-1]))
	}
}

// handleLineageClaimAncestors walks the claim-tree backward via the
// `sub_claim_handle_ids` chain in the claim_terminal record.
func handleLineageClaimAncestors(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		claimID, err := uuid.Parse(chi.URLParam(req, "claim_handle_id"))
		if err != nil {
			badRequest(w, "invalid claim_handle_id")
			return
		}
		depth := parseDepth(req)
		var (
			out      []persistence.LineageRow
			visited  = map[shared.UUID]struct{}{shared.UUID(claimID): {}}
			frontier = []shared.UUID{shared.UUID(claimID)}
		)
		for level := 0; level < depth && len(frontier) > 0; level++ {
			next := []shared.UUID{}
			for _, id := range frontier {
				records, err := deps.Persist.Lineage().GetByClaimHandleID(req.Context(), id)
				if err != nil {
					writeError(w, err)
					return
				}
				for _, r := range records {
					out = append(out, r)
					var rec struct {
						SubClaimHandleIDs []string `json:"sub_claim_handle_ids"`
					}
					if err := json.Unmarshal(r.Record, &rec); err != nil {
						continue
					}
					for _, sub := range rec.SubClaimHandleIDs {
						if u, err := uuid.Parse(sub); err == nil {
							uu := shared.UUID(u)
							if _, seen := visited[uu]; !seen {
								visited[uu] = struct{}{}
								next = append(next, uu)
							}
						}
					}
				}
			}
			frontier = next
		}
		items := make([]lineageRecordItem, 0, len(out))
		for _, lr := range out {
			items = append(items, toLineageItem(lr))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ancestors": items,
			"depth":     depth,
		})
	}
}

// handleLineageBySource implements the reverse lookup. Operators ask
// "which leaf-run records cite this (source_kind, source_version_or_id)
// as a substitution ref?". V1 implementation queries the lineage
// projection and filters in Go — the postgres GIN-index acceleration
// noted in the plan is a follow-up under K (OpenLineage subscriber)
// where bulk traversals are the primary workload.
func handleLineageBySource(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		sourceKind := chi.URLParam(req, "source_type")
		sourceID := chi.URLParam(req, "source_id")
		if sourceKind == "" || sourceID == "" {
			badRequest(w, "source_type and source_id required")
			return
		}
		page, err := deps.Persist.Lineage().Query(req.Context(), persistence.LineageQuery{
			Kind: persistence.LineageRecordKindLeafRun,
		}, persistence.ListPagination{Limit: 500})
		if err != nil {
			writeError(w, err)
			return
		}
		matches := make([]lineageRecordItem, 0)
		for _, r := range page.Rows {
			if recordMentionsSource(r.Record, sourceKind, sourceID) {
				matches = append(matches, toLineageItem(r))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"records": matches,
		})
	}
}

func recordMentionsSource(record json.RawMessage, kind, id string) bool {
	if len(record) == 0 {
		return false
	}
	var rec struct {
		SubstitutionRefs []struct {
			SourceKind        string `json:"source_kind"`
			SourceVersionOrID string `json:"source_version_or_id"`
		} `json:"substitution_refs"`
	}
	if err := json.Unmarshal(record, &rec); err != nil {
		return false
	}
	for _, r := range rec.SubstitutionRefs {
		if r.SourceKind == kind && r.SourceVersionOrID == id {
			return true
		}
	}
	return false
}

// handleLineageByProducer returns claim_terminal records emitted by the
// named producer. Optional `?version=` narrows to a specific version_id.
func handleLineageByProducer(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		producer := chi.URLParam(req, "executor_name")
		if producer == "" {
			badRequest(w, "executor_name required")
			return
		}
		version := req.URL.Query().Get("version")
		page, err := deps.Persist.Lineage().Query(req.Context(), persistence.LineageQuery{
			Kind: persistence.LineageRecordKindClaimTerminal,
		}, persistence.ListPagination{Limit: 500})
		if err != nil {
			writeError(w, err)
			return
		}
		matches := make([]lineageRecordItem, 0)
		for _, r := range page.Rows {
			if recordMentionsProducer(r.Record, producer, version) {
				matches = append(matches, toLineageItem(r))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"records": matches,
		})
	}
}

func recordMentionsProducer(record json.RawMessage, name, version string) bool {
	if len(record) == 0 {
		return false
	}
	var rec struct {
		ProducerName string `json:"producer_name"`
		VersionID    string `json:"version_id"`
	}
	if err := json.Unmarshal(record, &rec); err != nil {
		return false
	}
	if rec.ProducerName != name {
		return false
	}
	if version != "" && rec.VersionID != version {
		return false
	}
	return true
}
