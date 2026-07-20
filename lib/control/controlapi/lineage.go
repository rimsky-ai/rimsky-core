// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage-record

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
	lineageWalkDefaultDepth     = 3
	lineageWalkMaxDepth         = 50
	lineageWalkPerFrontierLimit = 1000
)

func registerLineageRoutes(r chi.Router, deps AppDeps) {
	r.Get("/lineage/runs/{run_id}", gate(deps, "lineage:read", handleLineageRun(deps)))
	r.Get("/lineage/runs/{run_id}/ancestors", gate(deps, "lineage:read", handleLineageRunAncestors(deps)))
	r.Get("/lineage/runs/{run_id}/descendants", gate(deps, "lineage:read", handleLineageRunDescendants(deps)))
	r.Get("/lineage/claims/{claim_handle_id}", gate(deps, "lineage:read", handleLineageClaim(deps)))
	r.Get("/lineage/claims/{claim_handle_id}/ancestors", gate(deps, "lineage:read", handleLineageClaimAncestors(deps)))
	r.Get("/lineage/claims/{claim_handle_id}/descendants", gate(deps, "lineage:read", handleLineageClaimDescendants(deps)))
	r.Get("/lineage/by-source/{source_type}/{source_id}", gate(deps, "lineage:read", handleLineageBySource(deps)))
	r.Get("/lineage/by-producer/{executor_name}", gate(deps, "lineage:read", handleLineageByProducer(deps)))
	r.Post("/admin/lineage/prune", gate(deps, "lineage:prune", handleLineagePrune(deps)))
}

type pruneLineageRequest struct {
	Before string `json:"before"`
}

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
		if ModeFromContext(req.Context()) == authModeDryRun {
			n, err := deps.Persist.Lineage().CountOlderThan(req.Context(), cutoff)
			if err != nil {
				writeError(w, err)
				return
			}
			WriteDryRunResponseForced(w, "would_have_pruned", map[string]any{
				"before": body.Before,
				"count":  n,
			})
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
		writeJSON(w, http.StatusOK, toLineageItem(rows[len(rows)-1]))
	}
}

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

type lineageWalkDirection int

const (
	lineageWalkDirectionAncestors lineageWalkDirection = iota
	lineageWalkDirectionDescendants
)

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
						if len(ancestorRows) > 0 {
							out = append(out, ancestorRows[len(ancestorRows)-1])
						}
						next = append(next, refID)
					}
				}
			case lineageWalkDirectionDescendants:
				children, err := deps.Persist.Lineage().QueryByParentNodeRunID(ctx, id, lineageWalkPerFrontierLimit)
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
		if r.SourceKind != "run" {
			continue
		}
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
		NodeRunID string `json:"run_id"`
	}
	if err := json.Unmarshal(record, &rec); err != nil {
		return shared.UUID{}
	}
	if u, err := uuid.Parse(rec.NodeRunID); err == nil {
		return shared.UUID(u)
	}
	return shared.UUID{}
}

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

// @concept: lineage-record
func walkLineageClaims(
	ctx context.Context, deps AppDeps,
	seed shared.UUID, depth int, dir lineageWalkDirection,
) ([]persistence.LineageRow, error) {
	visited := map[shared.UUID]struct{}{seed: {}}
	var out []persistence.LineageRow
	frontier := []shared.UUID{seed}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		next := []shared.UUID{}
		for _, id := range frontier {
			records, err := deps.Persist.Lineage().GetByClaimHandleID(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, r := range records {
				out = append(out, r)
				var rec struct {
					ParentClaimHandleID *string  `json:"parent_claim_handle_id"`
					SubClaimHandleIDs   []string `json:"sub_claim_handle_ids"`
				}
				if err := json.Unmarshal(r.Record, &rec); err != nil {
					continue
				}
				var neighbors []string
				switch dir {
				case lineageWalkDirectionAncestors:
					if rec.ParentClaimHandleID != nil {
						neighbors = []string{*rec.ParentClaimHandleID}
					}
				case lineageWalkDirectionDescendants:
					neighbors = rec.SubClaimHandleIDs
				}
				for _, n := range neighbors {
					if u, err := uuid.Parse(n); err == nil {
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
	return out, nil
}

// @concept: lineage-record
func handleLineageClaimAncestors(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		claimID, err := uuid.Parse(chi.URLParam(req, "claim_handle_id"))
		if err != nil {
			badRequest(w, "invalid claim_handle_id")
			return
		}
		depth := parseDepth(req)
		out, err := walkLineageClaims(req.Context(), deps, shared.UUID(claimID), depth, lineageWalkDirectionAncestors)
		if err != nil {
			writeError(w, err)
			return
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

// @concept: lineage-record
func handleLineageClaimDescendants(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		claimID, err := uuid.Parse(chi.URLParam(req, "claim_handle_id"))
		if err != nil {
			badRequest(w, "invalid claim_handle_id")
			return
		}
		depth := parseDepth(req)
		out, err := walkLineageClaims(req.Context(), deps, shared.UUID(claimID), depth, lineageWalkDirectionDescendants)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]lineageRecordItem, 0, len(out))
		for _, lr := range out {
			items = append(items, toLineageItem(lr))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"descendants": items,
			"depth":       depth,
		})
	}
}

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
