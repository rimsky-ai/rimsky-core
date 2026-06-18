// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type nodeResponse struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	NodeType   string `json:"node_type"`
	Executor   string `json:"executor,omitempty"`
	State      string `json:"state"`
	SettlingSignalType   string `json:"settling_signal_type,omitempty"`
	CurrentErrorClass    string `json:"current_error_class,omitempty"`
	RetryCounter         int    `json:"retry_counter"`
	ActionIndex          int    `json:"action_index"`
	AssignedSupervisorID string `json:"assigned_supervisor_id,omitempty"`
	FrameID              string `json:"frame_id,omitempty"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LatestAttributes map[string]any `json:"latest_attributes,omitempty"`
}

func toNodeResponse(n persistence.NodeRow) nodeResponse {
	frameID := ""
	if n.FrameID != nil {
		frameID = n.FrameID.String()
	}
	tags := n.Tags
	if tags == nil {
		tags = []string{}
	}
	settlingSig := ""
	if n.SettlingSignalType != nil {
		settlingSig = *n.SettlingSignalType
	}
	return nodeResponse{
		ID:                   n.ID.String(),
		InstanceID:           n.InstanceID.String(),
		NodeType:             n.NodeType,
		Executor:             n.Executor,
		State:                string(n.State),
		SettlingSignalType:   settlingSig,
		CurrentErrorClass:    n.CurrentErrorClass,
		RetryCounter:         n.RetryCounter,
		ActionIndex:          n.ActionIndex,
		AssignedSupervisorID: n.AssignedSupervisorID,
		FrameID:              frameID,
		Tags:                 tags,
		CreatedAt:            n.CreatedAt,
		UpdatedAt:            n.UpdatedAt,
	}
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
		var row *persistence.NodeRow
		var latestBag map[string]any
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Nodes().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			row = r
			if row == nil {
				return nil
			}
			inst, err := deps.Persist.Instances().Get(ctx, row.InstanceID, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return nil
			}
			attrs, err := deps.Persist.NodeAttributes().GetLatestByNode(ctx, row.ID, inst.MainRunScopeID, tx)
			if err != nil {
				return err
			}
			if attrs != nil {
				latestBag = attrs.Data
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
		resp := toNodeResponse(*row)
		resp.LatestAttributes = latestBag
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleResetNode(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		var row *persistence.NodeRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Nodes().Get(ctx, id, tx)
			row = r
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrNodeNotFound.Error())
			return
		}
		if row.State != cascade.NodeStateFailed {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "reset only valid from state=failed",
				"state": string(row.State),
			})
			return
		}
		if WriteDryRunResponse(w, req, "would_have_reset", map[string]any{
			"node_id": id.String(),
		}) {
			return
		}
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if err := deps.Persist.Nodes().UpdateError(ctx, id, node.EvaluatorState{}, tx); err != nil {
				return err
			}
			scopeID, err := deps.Persist.Nodes().GetFailedTerminalRunScopeID(ctx, id, tx)
			if err != nil {
				return err
			}
			if scopeID != nil {
				if err := deps.Persist.Nodes().ResetFailedTerminalSettlingSignalType(ctx, id, *scopeID, tx); err != nil {
					return err
				}
			}
			return deps.Persist.Nodes().SetFrameID(ctx, id, nil, tx)
		}); err != nil {
			writeError(w, err)
			return
		}
		// @story: node-admin
		// @decision: node-reset-as-pure-retry-budget-clear
		// @constraint: reset is a pure retry-budget-clear verb. No
		// envelope is synthesized, no frame is opened. The operator's
		// workflow for retrying an errored node is two explicit
		// steps: reset (clears the retry budget), then a message
		// (empty or typed) that invalidates the node so a fresh
		// dispatch is attempted.
		// @constraint: the reset audit event carries the owning instance_id so it
		// surfaces on the instance-scoped /v1/events feed; without it, the
		// row is dropped by the events read filter and the operator's
		// instance-scoped audit trail loses the reset action.
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
		}); err != nil && deps.Logger != nil {
			deps.Logger.Warn("handleResetNode: append operator_override audit event failed",
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
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Nodes().ListByInstancePagedFiltered(ctx, inst.ID,
				persistence.ListPagination{Limit: limit, Cursor: cursor},
				persistence.NodeListFilter{Tag: tagFilter}, tx)
			page = p
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		out := make([]nodeResponse, 0, len(page.Rows))
		for _, n := range page.Rows {
			out = append(out, toNodeResponse(n))
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
		return out, nil
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
