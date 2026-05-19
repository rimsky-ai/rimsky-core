// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// backfills.go — F4. Backfill operation control-api surface.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Backfills / Control-api.
//
//   - POST /instances/{id}/backfills          — enqueue an invalidate-class
//                                                message carrying the
//                                                partition_request override.
//   - GET  /instances/{id}/backfills          — list recent backfills.
//   - GET  /backfills/{op_id}                 — single backfill status.
//   - GET  /backfills/{op_id}/partitions      — per-child-run drill-down.
//   - POST /backfills/{op_id}/cancel          — mark cancelled.
//
// @concept: backfill
//
// The control-api wraps `runtime.CreateBackfill`, `GetBackfillStatus`,
// and `CancelBackfill`. The drill-down endpoint walks the message's
// `frame_id` to enumerate per-child runs.

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/runtime"
)

func registerBackfillsRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{id}/backfills", gate(deps, "backfill:create", handleCreateBackfill(deps)))
	r.Get("/instances/{id}/backfills", gate(deps, "backfill:read", handleListBackfills(deps)))
	r.Get("/backfills/{op_id}", gate(deps, "backfill:read", handleGetBackfill(deps)))
	r.Get("/backfills/{op_id}/partitions", gate(deps, "backfill:read", handleBackfillPartitions(deps)))
	r.Post("/backfills/{op_id}/cancel", gate(deps, "backfill:cancel", handleCancelBackfill(deps)))
}

// createBackfillRequest is the POST /instances/{id}/backfills body.
type createBackfillRequest struct {
	TargetNode               string          `json:"target_node"`
	PartitionRequestOverride json.RawMessage `json:"partition_request_override,omitempty"`
	Reason                   string          `json:"reason,omitempty"`
}

type createBackfillResponse struct {
	MessageID           string `json:"message_id"`
	BackfillOperationID string `json:"backfill_operation_id"`
}

func handleCreateBackfill(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		var body createBackfillRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if body.TargetNode == "" {
			badRequest(w, "target_node is required")
			return
		}
		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		now := deps.Clock.Now().UTC()
		var created runtime.BackfillCreated
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			if inst.TerminatedAt != nil {
				return errInstanceTerminated
			}
			// Dry-run gate: instance exists and is not terminated.
			// Signal the outer code to write the synthetic envelope; the
			// tx rolls back without enqueuing the backfill message.
			if isDryRun {
				return errDryRunOK
			}
			c, err := runtime.CreateBackfill(ctx, tx, deps.Persist.Messages(), now, runtime.BackfillCreateRequest{
				InstanceID:               shared.UUID(instanceID),
				TargetNode:               body.TargetNode,
				PartitionRequestOverride: body.PartitionRequestOverride,
				Reason:                   body.Reason,
				Sender:                   "operator",
			})
			if err != nil {
				return err
			}
			created = c
			return nil
		})
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_created_backfill", map[string]any{
				"instance_id": instanceID.String(),
				"target_node": body.TargetNode,
				"reason":      body.Reason,
			})
			return
		}
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			if errors.Is(err, errInstanceTerminated) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, createBackfillResponse{
			MessageID:           created.MessageID.String(),
			BackfillOperationID: created.BackfillOperationID.String(),
		})
	}
}

// backfillItem is the projection of a backfill-class message for list
// responses. Mirrors `messageItem` but elides envelope-internal fields
// the operator does not need at the operations layer.
type backfillItem struct {
	OperationID string     `json:"operation_id"`
	MessageID   string     `json:"message_id"`
	TargetNode  string     `json:"target_node"`
	Reason      string     `json:"reason,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	FrameID     string     `json:"frame_id,omitempty"`
	Cancelled   bool       `json:"cancelled,omitempty"`
}

func toBackfillItem(r persistence.MessageRow) backfillItem {
	out := backfillItem{
		MessageID:   r.ID.String(),
		TargetNode:  r.Target,
		ReceivedAt:  r.ReceivedAt,
		DeliveredAt: r.DeliveredAt,
		Cancelled:   r.Cancelled,
	}
	if r.BackfillOperationID != nil {
		out.OperationID = r.BackfillOperationID.String()
	}
	if r.FrameID != nil {
		out.FrameID = r.FrameID.String()
	}
	out.Reason = extractBackfillReason(r.Payload)
	return out
}

// extractBackfillReason pulls the `reason` field out of the payload
// without inspecting the rest of the bytes (`@blessed-invariant 21`).
// Empty when absent or unparseable; the lookup is a JSON-decode against
// a single known key.
func extractBackfillReason(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Reason
}

func handleListBackfills(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		q := req.URL.Query()
		instUUID := shared.UUID(instanceID)
		filter := persistence.MessageListFilter{
			InstanceID: &instUUID,
			Kind:       "invalidate",
		}
		pag := persistence.ListPagination{
			Limit:  parseLimit(req, 100),
			Cursor: q.Get("cursor"),
		}
		page, err := deps.Persist.Messages().List(req.Context(), filter, pag)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]backfillItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			if r.BackfillOperationID == nil {
				// Plain invalidate (not a backfill); skip.
				continue
			}
			items = append(items, toBackfillItem(r))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"backfills":   items,
			"next_cursor": page.NextCursor,
		})
	}
}

// backfillStatusResponse is the body of GET /backfills/{op_id}. Mirrors
// `runtime.BackfillStatus` with operator-friendly field names.
type backfillStatusResponse struct {
	OperationID string     `json:"operation_id"`
	InstanceID  string     `json:"instance_id"`
	TargetNode  string     `json:"target_node"`
	Reason      string     `json:"reason,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	FrameID     string     `json:"frame_id,omitempty"`
	Cancelled   bool       `json:"cancelled,omitempty"`
}

func handleGetBackfill(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		opID, err := uuid.Parse(chi.URLParam(req, "op_id"))
		if err != nil {
			badRequest(w, "invalid op_id")
			return
		}
		status, err := runtime.GetBackfillStatus(req.Context(), nil, deps.Persist.Messages(), shared.UUID(opID))
		if err != nil {
			writeError(w, err)
			return
		}
		if status == nil {
			notFoundResp(w, "backfill not found")
			return
		}
		resp := backfillStatusResponse{
			OperationID: status.OperationID.String(),
			InstanceID:  status.InstanceID.String(),
			TargetNode:  status.TargetNode,
			Reason:      status.Reason,
			ReceivedAt:  status.ReceivedAt,
			DeliveredAt: status.DeliveredAt,
			Cancelled:   status.Cancelled,
		}
		if status.FrameID != nil {
			resp.FrameID = status.FrameID.String()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// backfillPartitionRow surfaces one child run beneath the backfill's
// parent run, keyed by the partition / iteration value
// (`col:rimsky_node_runs.child_key`).
type backfillPartitionRow struct {
	RunID       string `json:"run_id"`
	NodeID      string `json:"node_id"`
	ChildKey    string `json:"child_key,omitempty"`
	State       string `json:"state"`
	LastOutcome string `json:"last_outcome,omitempty"`
}

// handleBackfillPartitions returns per-child-run drill-down for the
// backfill's delivered frame. Looks up the message, resolves the parent
// run by (frame_id, target node), walks the run-tree's children.
func handleBackfillPartitions(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		opID, err := uuid.Parse(chi.URLParam(req, "op_id"))
		if err != nil {
			badRequest(w, "invalid op_id")
			return
		}
		status, err := runtime.GetBackfillStatus(req.Context(), nil, deps.Persist.Messages(), shared.UUID(opID))
		if err != nil {
			writeError(w, err)
			return
		}
		if status == nil {
			notFoundResp(w, "backfill not found")
			return
		}
		// Frame not yet delivered: nothing to drill into.
		if status.FrameID == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"partitions": []backfillPartitionRow{},
			})
			return
		}
		// Resolve target node — the target_node alias is a node_type
		// within the instance. Look up the node row by (instance_id,
		// node_type) and find the parent run.
		var partitions []backfillPartitionRow
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			node, err := findNodeByType(ctx, deps, tx, status.InstanceID, status.TargetNode)
			if err != nil {
				return err
			}
			if node == nil {
				// Target node missing (template churn). Empty partitions.
				return nil
			}
			parentRun, err := findRootRunInFrame(ctx, deps, tx, node.ID, *status.FrameID)
			if err != nil {
				return err
			}
			if parentRun == nil {
				return nil
			}
			children, err := deps.Persist.RunTree().ListChildren(ctx, tx, parentRun.RunID)
			if err != nil {
				return err
			}
			for _, c := range children {
				partitions = append(partitions, backfillPartitionRow{
					RunID:       c.RunID.String(),
					NodeID:      c.NodeID.String(),
					ChildKey:    c.ChildKey,
					State:       string(c.State),
					LastOutcome: string(c.LastOutcome),
				})
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if partitions == nil {
			partitions = []backfillPartitionRow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"partitions": partitions,
		})
	}
}

// findNodeByType is a small helper that finds the rimsky_nodes row for
// a (instance, node_type) pair. The NodeTable does not surface this
// query directly; we walk ListByInstance and match by type. Acceptable
// because the per-instance node count is small (template-defined).
func findNodeByType(
	ctx context.Context, deps AppDeps, tx persistence.Tx,
	instanceID shared.UUID, nodeType string,
) (*persistence.NodeRow, error) {
	nodes, err := deps.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NodeType == nodeType {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

// findRootRunInFrame finds the root (parent_run_id NULL) run row for a
// given (node_id, frame_id) pair via the run-tree accessor. There is
// no direct lookup in the interface; we walk children with a list and
// short-circuit. For backfill drill-down the call is bounded by the
// fan-out's partition count, which is operator-driven and modest.
//
// Implementation walks `rimsky_node_runs` via the persistence-layer
// helpers we already have. The dispatch-ledger Queue.SelectCandidates
// path joins frames + node_runs; we reproduce the lookup against the
// state-bearing run-tree row directly.
func findRootRunInFrame(
	ctx context.Context, deps AppDeps, tx persistence.Tx,
	nodeID shared.UUID, frameID shared.UUID,
) (*persistence.RunTreeRow, error) {
	// The run-tree interface does not expose a direct (node, frame)
	// lookup. For now we approximate by walking the node's in-flight
	// run via the existing NodeTable.Get (which projects the
	// most-relevant run row) and check whether its frame matches.
	node, err := deps.Persist.Nodes().Get(ctx, nodeID, tx)
	if err != nil {
		return nil, err
	}
	if node == nil || node.FrameID == nil || *node.FrameID != frameID {
		return nil, nil
	}
	// Node's most-relevant run row lines up with the frame; load via
	// the runtree by id is not directly available either. We expose
	// the run id from the NodeRow projection's most-relevant-run
	// lookup when GetByParentChildKey isn't usable. The current
	// NodeRow doesn't carry the run id, so we resort to a small
	// SELECT through the queue accessor.
	//
	// Pre-v1 acceptable: backfill drill-down is a low-volume operator
	// surface; the lookup uses the per-driver tables interface.
	return runTreeRowForNodeInFrame(ctx, deps, tx, nodeID, frameID)
}

// runTreeRowForNodeInFrame loads the run-tree row whose (node, frame)
// matches and `parent_run_id IS NULL`. Implemented by walking the
// node's children via the Queue's in-flight-run lookup helper.
func runTreeRowForNodeInFrame(
	ctx context.Context, deps AppDeps, tx persistence.Tx,
	nodeID shared.UUID, frameID shared.UUID,
) (*persistence.RunTreeRow, error) {
	runID, ok, err := deps.Queue.GetInFlightRunForNode(ctx, tx, nodeID, frameID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return deps.Persist.RunTree().GetByID(ctx, tx, runID)
}

func handleCancelBackfill(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		opID, err := uuid.Parse(chi.URLParam(req, "op_id"))
		if err != nil {
			badRequest(w, "invalid op_id")
			return
		}
		// Backfill-existence check must precede the dry-run gate so a
		// dry-run against a missing op_id returns the same 404 a real
		// call would. Per spec section "Dry-run mode": "Errors from
		// validation surface as in normal flow."
		status, err := runtime.GetBackfillStatus(req.Context(), nil, deps.Persist.Messages(), shared.UUID(opID))
		if err != nil {
			writeError(w, err)
			return
		}
		if status == nil {
			notFoundResp(w, "backfill not found")
			return
		}
		if WriteDryRunResponse(w, req, "would_have_cancelled_backfill", map[string]any{
			"op_id": opID.String(),
		}) {
			return
		}
		now := deps.Clock.Now().UTC()
		var affected int
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			n, err := runtime.CancelBackfill(ctx, tx, deps.Persist.Messages(), now, shared.UUID(opID))
			if err != nil {
				return err
			}
			affected = n
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"cancelled":       true,
			"messages_voided": affected,
		})
	}
}
