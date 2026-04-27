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

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

type nodeResponse struct {
	ID                   string     `json:"id"`
	InstanceID           string     `json:"instance_id"`
	NodeType             string     `json:"node_type"`
	Executor             string     `json:"executor,omitempty"`
	ScheduleCron         string     `json:"schedule_cron,omitempty"`
	State                string     `json:"state"`
	Dependencies         []string   `json:"dependencies"`
	CurrentErrorClass    string     `json:"current_error_class,omitempty"`
	RetryCounter         int        `json:"retry_counter"`
	ActionIndex          int        `json:"action_index"`
	LastHeartbeatAt      *time.Time `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string     `json:"assigned_supervisor_id,omitempty"`
	FrameID              string     `json:"frame_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func toNodeResponse(n storage.NodeRow) nodeResponse {
	deps := make([]string, 0, len(n.Dependencies))
	for _, d := range n.Dependencies {
		deps = append(deps, d.String())
	}
	frameID := ""
	if n.FrameID != nil {
		frameID = n.FrameID.String()
	}
	return nodeResponse{
		ID:                   n.ID.String(),
		InstanceID:           n.InstanceID.String(),
		NodeType:             n.NodeType,
		Executor:             n.Executor,
		ScheduleCron:         n.ScheduleCron,
		State:                string(n.State),
		Dependencies:         deps,
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

// invalidateNodeRequest carries the optional human-readable reason an operator
// supplies on POST /nodes/:id/invalidate. The pre-redesign `RestoreVersion`
// field was removed alongside the resource versioning system (spec §11.3).
type invalidateNodeRequest struct {
	Reason string `json:"reason,omitempty"`
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
		row, err := deps.Storage.Nodes().Get(req.Context(), id, nil)
		if err != nil {
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

		row, err := deps.Storage.Nodes().Get(req.Context(), id, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrNodeNotFound.Error())
			return
		}
		// Record the operator action in the audit log.
		_ = deps.Storage.Events().Append(req.Context(), storage.EventAppendInput{
			NodeID: &id,
			Kind:   "operator_override",
			Payload: map[string]any{
				"action": "invalidate",
				"reason": body.Reason,
			},
		}, nil)
		if err := scheduler.InvalidateNode(req.Context(), scheduler.InvalidateArgs{
			Storage:      deps.Storage,
			Queue:        deps.Queue,
			Clock:        deps.Clock,
			Logger:       deps.Logger,
			TargetNodeID: id,
			Reason:       "operator_override",
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
		row, err := deps.Storage.Nodes().Get(req.Context(), id, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrNodeNotFound.Error())
			return
		}
		if row.State != shared.NodeStateFailed {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "reset only valid from state=failed",
				"state": string(row.State),
			})
			return
		}
		// Clear error bookkeeping.
		if err := deps.Storage.Nodes().UpdateError(req.Context(), id, node.EvaluatorState{}, nil); err != nil {
			writeError(w, err)
			return
		}
		// Defensively clear the stale frame_id pointing at the previously-
		// failed frame; the frame engine will write the new frame's id at
		// frame-start.
		if err := deps.Storage.Nodes().SetFrameID(req.Context(), id, nil, nil); err != nil {
			writeError(w, err)
			return
		}
		// Drive the reset through the frame engine. EnqueueOrCoalesce in
		// 'serial_queue' mode creates a new queued frame; in 'coalesce'
		// mode it appends this node to the pending coalesce row (or
		// creates a new one). The source-eligibility predicate at
		// frame-start (advanceOneFrame) accepts state=failed.
		if err := deps.Storage.Transaction(req.Context(), func(ctx context.Context, stx storage.Tx) error {
			pgT, err := pgstorage.PgxTxFromStorage(stx)
			if err != nil {
				return err
			}
			_, err = frame.EnqueueOrCoalesce(ctx, pgT, row.InstanceID, row.ID)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		_ = deps.Storage.Events().Append(req.Context(), storage.EventAppendInput{
			NodeID: &id,
			Kind:   "operator_override",
			Payload: map[string]any{
				"action": "reset",
			},
		}, nil)
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
		page, err := deps.Storage.Nodes().ListByInstancePaged(req.Context(), inst.ID,
			storage.ListPagination{Limit: limit, Cursor: cursor}, nil)
		if err != nil {
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

// resolveInstance looks up an instance by UUID (preferred) or consumer_key
// (fallback). Returns nil, nil when not found.
func resolveInstance(ctx context.Context, deps AppDeps, idOrKey string) (*storage.InstanceRow, error) {
	if id, err := uuid.Parse(idOrKey); err == nil {
		return deps.Storage.Instances().Get(ctx, id, nil)
	}
	// Consumer-key resolution requires a template ID today; walk the instance
	// list filtered by consumer_key and return the first hit.
	page, err := deps.Storage.Instances().List(ctx, storage.InstanceListFilter{
		ConsumerKey: idOrKey,
	}, storage.ListPagination{Limit: 1}, nil)
	if err != nil {
		return nil, err
	}
	if len(page.Rows) == 0 {
		return nil, nil
	}
	row := page.Rows[0]
	return &row, nil
}
