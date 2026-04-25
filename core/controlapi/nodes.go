// nodes.go — GET /nodes/:id, POST /nodes/:id/invalidate,
// POST /nodes/:id/reset, POST /nodes/:id/kill, GET /instances/:id_or_key/nodes.
//
// Operator overrides (invalidate / reset / kill) emit an `operator_override`
// event so audits can see who drove the state change. Handlers return 404 when
// the node does not exist.
package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type nodeResponse struct {
	ID                   string     `json:"id"`
	InstanceID           string     `json:"instance_id"`
	NodeType             string     `json:"node_type"`
	Executor             string     `json:"executor,omitempty"`
	ScheduleCron         string     `json:"schedule_cron,omitempty"`
	State                string     `json:"state"`
	Dependencies         []string   `json:"dependencies"`
	ConcurrencyTags      []string   `json:"concurrency_tags"`
	CurrentErrorClass    string     `json:"current_error_class,omitempty"`
	RetryCounter         int        `json:"retry_counter"`
	ActionIndex          int        `json:"action_index"`
	LastHeartbeatAt      *time.Time `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string     `json:"assigned_supervisor_id,omitempty"`
	KillRequested        bool       `json:"kill_requested"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func toNodeResponse(n storage.NodeRow) nodeResponse {
	deps := make([]string, 0, len(n.Dependencies))
	for _, d := range n.Dependencies {
		deps = append(deps, d.String())
	}
	if n.ConcurrencyTags == nil {
		n.ConcurrencyTags = []string{}
	}
	return nodeResponse{
		ID:                   n.ID.String(),
		InstanceID:           n.InstanceID.String(),
		NodeType:             n.NodeType,
		Executor:             n.Executor,
		ScheduleCron:         n.ScheduleCron,
		State:                string(n.State),
		Dependencies:         deps,
		ConcurrencyTags:      n.ConcurrencyTags,
		CurrentErrorClass:    n.CurrentErrorClass,
		RetryCounter:         n.RetryCounter,
		ActionIndex:          n.ActionIndex,
		LastHeartbeatAt:      n.LastHeartbeatAt,
		AssignedSupervisorID: n.AssignedSupervisorID,
		KillRequested:        n.KillRequested,
		CreatedAt:            n.CreatedAt,
		UpdatedAt:            n.UpdatedAt,
	}
}

type invalidateNodeRequest struct {
	Reason         string `json:"reason,omitempty"`
	RestoreVersion string `json:"restore_version,omitempty"`
}

// registerNodesRoutes wires the /nodes and /instances/:id_or_key/nodes groups.
func registerNodesRoutes(r chi.Router, deps AppDeps) {
	r.Get("/nodes/{id}", handleGetNode(deps))
	r.Post("/nodes/{id}/invalidate", handleInvalidateNode(deps))
	r.Post("/nodes/{id}/reset", handleResetNode(deps))
	r.Post("/nodes/{id}/kill", handleKillNode(deps))
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
				"action":          "invalidate",
				"reason":          body.Reason,
				"restore_version": body.RestoreVersion,
			},
		}, nil)
		if err := scheduler.InvalidateNode(req.Context(), scheduler.InvalidateArgs{
			Storage:        deps.Storage,
			Queue:          deps.Queue,
			Clock:          deps.Clock,
			Logger:         deps.Logger,
			TargetNodeID:   id,
			Reason:         "operator_override",
			RestoreVersion: body.RestoreVersion,
		}); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

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
		// Transition failed → stale via operator_reset.
		if err := deps.Storage.Nodes().UpdateState(req.Context(), id, shared.NodeStateStale, node.ReasonOperatorReset, nil); err != nil {
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

func handleKillNode(deps AppDeps) http.HandlerFunc {
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
		// Flip the kill flag regardless of state — spec says no-op on
		// non-running nodes, but we still record the flag + audit event so a
		// subsequent dispatch sees it immediately.
		if err := deps.Storage.Nodes().SetKillRequested(req.Context(), id, true, nil); err != nil {
			writeError(w, err)
			return
		}
		_ = deps.Storage.Events().Append(req.Context(), storage.EventAppendInput{
			NodeID: &id,
			Kind:   "operator_override",
			Payload: map[string]any{
				"action": "kill",
				"state":  string(row.State),
			},
		}, nil)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"kill_requested": true,
		})
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
