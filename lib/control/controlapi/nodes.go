// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: node
// @decision: node-state-retired-from-operator-api
// @decision: frame-isolation-is-structural
type nodeResponse struct {
	ID                 string                      `json:"id"`
	InstanceID         string                      `json:"instance_id"`
	NodeType           string                      `json:"node_type"`
	Executor           string                      `json:"executor,omitempty"`
	Tags               []string                    `json:"tags"`
	CascadeMode        string                      `json:"cascade_mode"`
	CreatedAt          time.Time                   `json:"created_at"`
	LatestAttributes   map[string]any              `json:"latest_attributes,omitempty"`
	RunSummary         *persistence.NodeRunSummary `json:"run_summary,omitempty"`
	SettlingSignalType string                      `json:"settling_signal_type,omitempty"`
}

// @concept: node
func toNodeResponse(n persistence.NodeRow) nodeResponse {
	tags := n.Tags
	if tags == nil {
		tags = []string{}
	}
	return nodeResponse{
		ID:          n.ID.String(),
		InstanceID:  n.InstanceID.String(),
		NodeType:    n.NodeType,
		Executor:    n.Executor,
		Tags:        tags,
		CascadeMode: string(n.CascadeMode),
		CreatedAt:   n.CreatedAt,
	}
}

// @concept: node
func toNodeResponseWithSummary(n persistence.NodeRow, summary persistence.NodeRunSummary) nodeResponse {
	out := toNodeResponse(n)
	s := summary
	out.RunSummary = &s
	return out
}

func registerNodesRoutes(r chi.Router, deps AppDeps) {
	r.Get("/nodes/{id}", gate(deps, "node:read", handleGetNode(deps)))
	r.Post("/nodes/{id}/reset", gate(deps, "node:reset", handleResetNode(deps)))
	r.Get("/instances/{idOrKey}/nodes", gate(deps, "node:read", handleListInstanceNodes(deps)))
}

func handleGetNode(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		var (
			row                *persistence.NodeRow
			latestBag          map[string]any
			summary            persistence.NodeRunSummary
			settlingSignalType string
		)
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Nodes().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			row = r
			if row == nil {
				return nil
			}
			s, err := deps.Persist.Nodes().GetRunSummary(ctx, row.ID, tx)
			if err != nil {
				return err
			}
			summary = s
			latest, err := deps.Persist.Nodes().GetLatestRunForNode(ctx, row.ID, tx)
			if err != nil {
				return err
			}
			if latest != nil {
				attrs, err := deps.Persist.NodeAttributes().GetLatestByNode(ctx, row.ID, latest.RunScopeID, tx)
				if err != nil {
					return err
				}
				if attrs != nil {
					latestBag = attrs.Data
				}
			}
			if latest != nil && latest.SettlingSignalType != nil {
				settlingSignalType = *latest.SettlingSignalType
			}
			return nil
		}); err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrNodeNotFound.Error())
			return
		}
		resp := toNodeResponseWithSummary(*row, summary)
		resp.LatestAttributes = latestBag
		resp.SettlingSignalType = settlingSignalType
		writeJSON(w, http.StatusOK, resp)
	}
}

// @decision: node-state-retired-from-operator-api
func handleResetNode(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		var (
			row           *persistence.NodeRow
			failedScopeID *shared.UUID
		)
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Nodes().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			row = r
			if r == nil {
				return nil
			}
			sid, err := deps.Persist.Nodes().GetFailedTerminalRunScopeID(ctx, id, tx)
			if err != nil {
				return err
			}
			failedScopeID = sid
			return nil
		}); err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrNodeNotFound.Error())
			return
		}
		if failedScopeID == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "reset only valid when node has a failed terminal run in some scope",
			})
			return
		}
		if WriteDryRunResponse(w, req, "would_have_reset", map[string]any{
			"node_id": id.String(),
		}) {
			return
		}
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.Nodes().ResetFailedTerminalSettlingSignalType(ctx, id, *failedScopeID, tx)
		}); err != nil {
			writeError(w, err)
			return
		}
		// @story: node-admin
		// @decision: node-reset-clears-failure-marker
		resetInstanceID := row.InstanceID
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &resetInstanceID,
				NodeID:     &id,
				Kind:       events.KindOperatorOverride(),
				Payload: map[string]any{
					"action": "reset",
				},
			}, tx)
		}); err != nil {
			auditLogger := deps.Logger
			if auditLogger == nil {
				auditLogger = shared.NewSlogLogger(slog.Default())
			}
			auditLogger.Warn("handleResetNode: append operator_override audit event failed",
				"node_id", id.String(),
				"error", err.Error())
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleListInstanceNodes(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		cursor := req.URL.Query().Get("cursor")
		limit := parseLimit(req, 100)
		tagFilter := req.URL.Query().Get("tag")
		var page persistence.PaginatedListResult[persistence.NodeRow]
		summaryByID := map[shared.UUID]persistence.NodeRunSummary{}
		settlingByID := map[shared.UUID]string{}
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Nodes().ListByInstancePagedFiltered(ctx, inst.ID,
				persistence.ListPagination{Limit: limit, Cursor: cursor},
				persistence.NodeListFilter{Tag: tagFilter}, tx)
			if err != nil {
				return err
			}
			page = p
			nodeIDs := make([]shared.UUID, 0, len(page.Rows))
			for _, n := range page.Rows {
				nodeIDs = append(nodeIDs, n.ID)
			}
			summaryByID, err = deps.Persist.Nodes().GetRunSummaryForNodes(ctx, nodeIDs, tx)
			if err != nil {
				return err
			}
			latestByID, err := deps.Persist.Nodes().GetLatestRunForNodes(ctx, nodeIDs, tx)
			if err != nil {
				return err
			}
			for id, latest := range latestByID {
				if latest.SettlingSignalType != nil {
					settlingByID[id] = *latest.SettlingSignalType
				}
			}
			return nil
		}); err != nil {
			writeError(w, err)
			return
		}
		out := make([]nodeResponse, 0, len(page.Rows))
		for _, n := range page.Rows {
			r := toNodeResponseWithSummary(n, summaryByID[n.ID])
			r.SettlingSignalType = settlingByID[n.ID]
			out = append(out, r)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes":       out,
			"next_cursor": page.NextCursor,
		})
	}
}

func resolveInstance(ctx context.Context, deps AppDeps, idOrKey string) (*persistence.InstanceRow, error) {
	var out *persistence.InstanceRow
	if id, err := uuid.Parse(idOrKey); err == nil {
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Instances().Get(ctx, id, tx)
			out = r
			return err
		}); err != nil {
			return nil, err
		}
		if out != nil {
			return out, nil
		}
	}
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.Instances().FindAnyByInstanceKey(ctx, idOrKey, tx)
		out = r
		return err
	}); err != nil {
		return nil, err
	}
	return out, nil
}
