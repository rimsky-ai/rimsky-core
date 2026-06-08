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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type nodeResponse struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	NodeType   string `json:"node_type"`
	Executor   string `json:"executor,omitempty"`
	State      string `json:"state"`
	// SettlingSignalType is the canonical signal type-path of the node's
	// settling resolution (concept:signal), projected from the persisted
	// rimsky_node_runs.settling_signal_type column via NodeRow. Empty (and
	// dropped by omitempty) while the node is unsettled / in-flight, where
	// the projected column is NULL. Mirrors backfills.go::backfillPartitionRow.
	SettlingSignalType   string     `json:"settling_signal_type,omitempty"`
	CurrentErrorClass    string     `json:"current_error_class,omitempty"`
	RetryCounter         int        `json:"retry_counter"`
	ActionIndex          int        `json:"action_index"`
	LastHeartbeatAt      *time.Time `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string     `json:"assigned_supervisor_id,omitempty"`
	FrameID              string     `json:"frame_id,omitempty"`
	// Tags is operator-facing metadata projected at instance creation
	// (per spec 2026-05-19 Item 4). Always emitted as an array; empty
	// means "no tags".
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// LatestAttributes is the node's most-recent resolved attribute bag —
	// its forensic last-attribute snapshot, the Data map of the row the
	// persistence primitive NodeAttributes().GetLatestByNode returns for
	// (node, main run scope). omitempty drops the key when no attribute
	// row exists yet (a never-executed node), so it is absent rather than
	// an empty object. Populated only on the GET /nodes/{id} detail read,
	// not on the list surface.
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
	// Deref the *string settling signal type, leaving "" when nil so
	// omitempty drops the key for an unsettled node. Mirrors the
	// projection in backfills.go::backfillPartitionRow.
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
		LastHeartbeatAt:      n.LastHeartbeatAt,
		AssignedSupervisorID: n.AssignedSupervisorID,
		FrameID:              frameID,
		Tags:                 tags,
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
	r.Get("/nodes/{id}", gate(deps, "node:read", handleGetNode(deps)))
	r.Post("/nodes/{id}/invalidate", gate(deps, "node:invalidate", handleInvalidateNode(deps)))
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
		// latestBag is the node's most-recent resolved attribute bag,
		// resolved in the SAME read tx as the node row so the detail and
		// its forensic snapshot are read atomically from one consistent
		// snapshot. nil → the node has never executed (or its row was not
		// yet committed); omitempty drops the key on the response.
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
			// Resolve the node's instance to its main run scope, then read
			// the latest per-run attribute bag for (node, main run scope).
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
		if WriteDryRunResponse(w, req, "would_have_invalidated", map[string]any{
			"node_id": id.String(),
			"reason":  body.Reason,
			"frame":   body.Frame,
		}) {
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
		// Operator-sourced `frame: in` (concept:cascade / concept:invalidate):
		// resolve the target instance's currently-running cascade frame and
		// thread it as SourceFrameID so invalidateInFrame joins THAT frame
		// (the open drain) rather than falling back to next-frame. The
		// operator invalidate has no source node, so SourceFrameID is the
		// authoritative input — invalidateInFrame's resolution order prefers
		// it and skips the source-node re-read when the caller supplies the
		// frame. When no frame is currently running, sourceFrameID stays nil
		// and invalidateInFrame takes its documented deterministic next-frame
		// fallback (the story's required behavior for an idle instance).
		var sourceFrameID *shared.UUID
		if body.Frame == "in" {
			if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
				fid, err := deps.Persist.Frames().GetRunningFrameID(ctx, row.InstanceID, tx)
				sourceFrameID = fid
				return err
			}); err != nil {
				writeError(w, err)
				return
			}
		}
		if err := runtime.InvalidateNode(req.Context(), runtime.InvalidateArgs{
			Persist:       deps.Persist,
			Queue:         deps.Queue,
			Clock:         deps.Clock,
			Logger:        deps.Logger,
			TargetNodeID:  id,
			SourceFrameID: sourceFrameID,
			Reason:        "operator_override",
			Frame:         body.Frame,
			Metrics:       deps.Metrics,
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
		if WriteDryRunResponse(w, req, "would_have_reset", map[string]any{
			"node_id": id.String(),
		}) {
			return
		}
		// Clear error bookkeeping + defensively clear stale frame_id +
		// reset the failed-terminal row's settling_signal_type in one tx.
		// Resetting settling_signal_type on the failed-terminal row
		// means the dashboard's `nodeSelect` projection (which surfaces
		// the failed-terminal row's signal type when no in-flight row
		// exists) no longer shows the stale failed resolution flavor.
		// Without this, the prior ClearSettlingSignalType(runID=nil)
		// would be a no-op against the failed-terminal row (its
		// predicate `phase IN ('pending','active','held','parked')`
		// excludes `phase='failed'`).
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if err := deps.Persist.Nodes().UpdateError(ctx, id, node.EvaluatorState{}, tx); err != nil {
				return err
			}
			// Resolve the failed-terminal row's RunScope so the reset
			// keys on the correct row. NodeRow.RunScopeID is the
			// in-flight scope (nil for a failed node), so look up the
			// scope of the most-recent failed-terminal run directly.
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
		// Per spec 2026-05-19 Item 4: single-value `?tag=` exact-match
		// filter. Multi-tag combinations are not in v1.
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
