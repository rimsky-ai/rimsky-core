// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// nodes.go — GET /nodes/:id, POST /nodes/:id/reset,
// GET /instances/:id_or_key/nodes.
//
// The reset override emits an `operator_override` event so audits can
// see who drove the state change. The kill route was removed by the
// frame-resolution redesign (spec §5.4); the operator-invalidate route
// retired with the 2026-06-14 message-schema-layer reshape (operators
// who want to invalidate post a typed message via
// `POST /instances/{id}/messages` with a template-declared
// `messages:` type; ad-hoc force-stale lives at the gated
// `POST /debug/override` endpoint).
// Handlers return 404 when the node does not exist.
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
	// the projected column is NULL.
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
	// @constraint: deref the *string settling signal type, leaving "" when nil so
	// omitempty drops the key for an unsettled node.
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

// registerNodesRoutes wires the /nodes and /instances/:id_or_key/nodes groups.
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
			// @constraint: resolve the node's instance to its main run scope, then read
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

// handleResetNode drives a failed node back into the engine via
// frame.EnqueueFrame. Direct UpdateState(failed → stale) bypassing
// the frame model would strand the node with no frame_id (blessed-
// invariant 19) — the scheduler's ready sweeps and recalculate.go
// explicitly skip nodes with nil frame_id, and the node would never run.
//
// The handler:
//  1. Clears error bookkeeping (action_index/retry_counter/error_class).
//  2. Defensively clears the stale frame_id pointing at the failed frame.
//  3. Calls frame.EnqueueFrame so the next scheduler tick advances
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
		// @constraint: clear error bookkeeping + defensively clear stale frame_id +
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
			// @constraint: resolve the failed-terminal row's RunScope so the reset
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
		// @constraint: drive the reset through the frame engine. Every frame carries a
		// triggering message (col:rimsky_frames.triggering_message_id is NOT NULL
		// post-migration-010); the reset has no external triggering envelope,
		// so it seeds a synthetic reset message and uses that as the frame's
		// origin — atomic in one tx so a crash mid-flow cannot leave a frame
		// with no envelope nor an envelope with no frame. The source-
		// eligibility predicate at frame-start (advanceOneFrame) accepts
		// state=failed.
		//
		// @constraint: the synthetic payload carries `wake_node_ids` (the typed-
		// message replacement for the retired source_node_ids column); the
		// frame engine reads this list at promotion and stale-marks each in
		// the promotion tx, preserving the ordering the supervisor relies on.
		//
		// @deliberate: `sender_kind: "instance"` matches the runtime-synthetic-
		// envelope convention even though the reset was operator-INITIATED —
		// the envelope body is runtime-synthesized (the operator did not
		// author the wake_node_ids list), so the discriminator marks it as
		// instance-side per runner_emit_message.go::emitCascadeMessageInTx.
		shouldID := shared.UUID(id)
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			_, _, err := runtime.EnqueueSyntheticWakeFrame(ctx, tx, deps.Persist,
				row.InstanceID, "node/reset", "",
				[]shared.UUID{shouldID}, nil)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
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
		// @constraint: per spec 2026-05-19 Item 4: single-value `?tag=` exact-match
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
	// @constraint: instance_key resolution: dedicated dispatch — there's no
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
