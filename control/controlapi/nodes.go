// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// nodes.go — GET /nodes/:id, POST /nodes/:id/invalidate,
// POST /nodes/:id/reset, GET /instances/:id_or_key/nodes.
//
// Operator overrides (invalidate / reset) emit an `operator_override`
// event so audits can see who drove the state change. The kill route was
// removed by the frame-resolution redesign (spec §5.4): operator
// invalidates enqueue/coalesce a frame; in-flight work is never preempted.
// Handlers return 404 when the node does not exist.
package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/frame"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/runtime"
)

type nodeResponse struct {
	ID                   string     `json:"id"`
	InstanceID           string     `json:"instance_id"`
	NodeType             string     `json:"node_type"`
	Executor             string     `json:"executor,omitempty"`
	State                string     `json:"state"`
	CurrentErrorClass    string     `json:"current_error_class,omitempty"`
	RetryCounter         int        `json:"retry_counter"`
	ActionIndex          int        `json:"action_index"`
	LastHeartbeatAt      *time.Time `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string     `json:"assigned_supervisor_id,omitempty"`
	FrameID              string     `json:"frame_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func toNodeResponse(n persistence.NodeRow) nodeResponse {
	frameID := ""
	if n.FrameID != nil {
		frameID = n.FrameID.String()
	}
	return nodeResponse{
		ID:                   n.ID.String(),
		InstanceID:           n.InstanceID.String(),
		NodeType:             n.NodeType,
		Executor:             n.Executor,
		State:                string(n.State),
		CurrentErrorClass:    n.CurrentErrorClass,
		RetryCounter:         n.RetryCounter,
		ActionIndex:          n.ActionIndex,
		LastHeartbeatAt:      n.LastHeartbeatAt,
		AssignedSupervisorID: n.AssignedSupervisorID,
		FrameID:              frameID,
		CreatedAt:            n.CreatedAt,
		UpdatedAt:            n.UpdatedAt,
	}
}

// invalidateNodeRequest carries the optional human-readable reason an
// operator supplies on POST /nodes/:id/invalidate.
//
// Frame controls whether the invalidate joins the current cascade
// ("in") or buffers through frame.EnqueueOrCoalesce as a new frame
// ("next"; default). See the reactive-loops + lifecycle-handlers
// spec §5.
type invalidateNodeRequest struct {
	Reason string `json:"reason,omitempty"`
	Frame  string `json:"frame,omitempty"` // "" | "in" | "next"; default "next"
}

// registerNodesRoutes wires the /nodes and /instances/:id_or_key/nodes groups.
func registerNodesRoutes(r chi.Router, deps AppDeps) {
	r.Get("/nodes/{id}", handleGetNode(deps))
	r.Post("/nodes/{id}/invalidate", handleInvalidateNode(deps))
	r.Post("/nodes/{id}/reset", handleResetNode(deps))
	r.Get("/instances/{idOrKey}/nodes", handleListInstanceNodes(deps))
}

func handleGetNode(deps AppDeps) http.HandlerFunc {
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
		writeJSON(w, http.StatusOK, toNodeResponse(*row))
	}
}

func handleInvalidateNode(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		var body invalidateNodeRequest
		// Body is optional; ignore decode error when body is empty.
		_ = json.NewDecoder(req.Body).Decode(&body)
		// Validate Frame; reject anything other than "" | "in" | "next".
		switch body.Frame {
		case "", "in", "next":
		default:
			badRequest(w, "frame must be \"in\" or \"next\"")
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
		// Record the operator action in the audit log.
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &id,
				Kind:   "operator_override",
				Payload: map[string]any{
					"action": "invalidate",
					"reason": body.Reason,
				},
			}, tx)
		}); err != nil && deps.Logger != nil {
			deps.Logger.Warn("handleInvalidateNode: append operator_override audit event failed",
				"node_id", id.String(),
				"reason", body.Reason,
				"error", err.Error())
		}
		if err := runtime.InvalidateNode(req.Context(), runtime.InvalidateArgs{
			Persist:      deps.Persist,
			Queue:        deps.Queue,
			Clock:        deps.Clock,
			Logger:       deps.Logger,
			TargetNodeID: id,
			Reason:       "operator_override",
			Frame:        body.Frame,
			Metrics:      deps.Metrics,
		}); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// handleResetNode drives a failed node back into the engine via
// frame.EnqueueOrCoalesce. Direct UpdateState(failed → stale) bypassing
// the frame model would strand the node with no frame_id (blessed-
// invariant 19) — the scheduler's ready sweeps and recalculate.go
// explicitly skip nodes with nil frame_id, and the node would never run.
//
// The handler:
//  1. Clears error bookkeeping (action_index/retry_counter/error_class).
//  2. Defensively clears the stale frame_id pointing at the failed frame.
//  3. Calls frame.EnqueueOrCoalesce so the next scheduler tick advances
//     the queued frame and writes the source node stale + new frame_id.
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
		// Clear error bookkeeping + defensively clear stale frame_id +
		// clear last_outcome in one tx. Clearing last_outcome here means
		// the dashboard does not show a stale `failed` resolution
		// flavor while the node transitions back through stale →
		// running → fresh — UpdateState's COALESCE pattern preserves
		// last_outcome on stale → running, so without this clear, a
		// failed → stale → running → (fresh+changed) sequence would
		// briefly display state=stale, last_outcome=failed.
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if err := deps.Persist.Nodes().UpdateError(ctx, id, node.EvaluatorState{}, tx); err != nil {
				return err
			}
			if err := deps.Persist.Nodes().ClearLastOutcome(ctx, id, tx); err != nil {
				return err
			}
			return deps.Persist.Nodes().SetFrameID(ctx, id, nil, tx)
		}); err != nil {
			writeError(w, err)
			return
		}
		// Drive the reset through the frame engine. EnqueueOrCoalesce in
		// 'serial_queue' mode creates a new queued frame; in 'coalesce'
		// mode it appends this node to the pending coalesce row (or
		// creates a new one). The source-eligibility predicate at
		// frame-start (advanceOneFrame) accepts state=failed.
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			_, err := frame.EnqueueOrCoalesce(ctx, deps.Persist, tx, row.InstanceID, row.ID)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &id,
				Kind:   "operator_override",
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
		var page persistence.PaginatedListResult[persistence.NodeRow]
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Nodes().ListByInstancePaged(ctx, inst.ID,
				persistence.ListPagination{Limit: limit, Cursor: cursor}, tx)
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

// resolveInstance looks up an instance by UUID (preferred) or instance_key
// (fallback). Returns nil, nil when not found.
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
	// instance_key resolution: dedicated dispatch — there's no
	// (template_hash, instance_key) on this URL, so use the
	// instance-key-only lookup.
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.Instances().FindAnyByInstanceKey(ctx, idOrKey, tx)
		out = r
		return err
	}); err != nil {
		return nil, err
	}
	return out, nil
}
