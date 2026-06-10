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
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// errBackfillTargetInvalid is returned from inside the create-backfill
// transaction when `target_node` is not a fan-out node wired to accept
// the partition override. It carries the operator-facing reason and is
// mapped to a 400 by the outer handler.
//
// Load-bearing property (`@concept: backfill`): a backfill against a
// target that cannot consume `partition_request_override` is REJECTED
// at submit, never accepted-and-silently-degraded to a plain invalidate
// that processes the template default. The check fires identically on
// the live and dry-run paths.
type errBackfillTargetInvalid struct {
	reason string
}

func (e errBackfillTargetInvalid) Error() string { return e.reason }

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

// validateBackfillTarget confirms `targetNode` is a fan-out node in the
// template that is wired to consume a `partition_request_override`. The
// runtime defers this validation to the control-api layer
// (`@concept: backfill`); this is where it lands.
//
// Rejects (returns errBackfillTargetInvalid) when:
//   - the target node is not declared in the template, OR
//   - the node declares no `fan_out` block (a backfill is meaningless
//     without a partition, and thus a fan-out), OR
//   - the node's `fan_out.partition_request` does not reference the
//     trigger message (`{{trigger.message.payload…}}`), so the override
//     would be ignored and the template default would silently fire.
//
// The trigger-substitution check is the subtle one and the load-bearing
// part of this validation: a fan-out node whose `partition_request` is a
// fixed literal cannot consume the override. We reject it rather than
// accept-and-silently-degrade. The detector mirrors the runtime
// resolver's notion of a trigger directive exactly (see
// attributes.ReferencesTriggerMessage).
func validateBackfillTarget(tpl spec.TemplateSpec, targetNode string) error {
	var found *spec.TemplateNodeDef
	for i := range tpl.Nodes {
		if tpl.Nodes[i].Type == targetNode {
			found = &tpl.Nodes[i]
			break
		}
	}
	if found == nil {
		return errBackfillTargetInvalid{reason: fmt.Sprintf(
			"target_node %q is not declared in the instance's template", targetNode)}
	}
	if found.FanOut == nil {
		return errBackfillTargetInvalid{reason: fmt.Sprintf(
			"target_node %q is not a fan-out node; a backfill requires a fan-out node whose partition_request is wired for the override", targetNode)}
	}
	if !attributes.ReferencesTriggerMessage(found.FanOut.PartitionRequest) {
		return errBackfillTargetInvalid{reason: fmt.Sprintf(
			"target_node %q fan-out is not wired for the override: its partition_request must reference the trigger message (e.g. {{trigger.message.payload.partition_request_override | <default>}}) to consume partition_request_override", targetNode)}
	}
	return nil
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
			// Validate the target is a fan-out node wired for the
			// override BEFORE the dry-run gate, so the live and dry-run
			// paths reject an invalid target identically (a bad target
			// fails the same way in preview). `@concept: backfill`.
			tpl, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return err
			}
			if tpl == nil {
				return shared.ErrTemplateNotFound
			}
			if err := validateBackfillTarget(tpl.Spec, body.TargetNode); err != nil {
				return err
			}
			// Dry-run gate: instance exists, is not terminated, and the
			// target validated. Signal the outer code to write the
			// synthetic envelope; the tx rolls back without enqueuing the
			// backfill message.
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
			var targetErr errBackfillTargetInvalid
			if errors.As(err, &targetErr) {
				badRequest(w, targetErr.reason)
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
	RunID              string `json:"run_id"`
	NodeID             string `json:"node_id"`
	ChildKey           string `json:"child_key,omitempty"`
	State              string `json:"state"`
	SettlingSignalType string `json:"settling_signal_type,omitempty"`
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
				// Resolve partition_key from the child's RunScope —
				// RunTreeRow no longer projects child_key inline.
				partitionKey := ""
				if scope, _ := deps.Persist.RunScopes().GetByID(ctx, tx, c.RunScopeID); scope != nil {
					partitionKey = scope.PartitionKey
				}
				var settlingSig string
				if c.SettlingSignalType != nil {
					settlingSig = *c.SettlingSignalType
				}
				partitions = append(partitions, backfillPartitionRow{
					RunID:              c.RunID.String(),
					NodeID:             c.NodeID.String(),
					ChildKey:           partitionKey,
					State:              string(c.State),
					SettlingSignalType: settlingSig,
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

// findRootRunInFrame finds the root run row (the fan-out parent's
// run) for a given (node_id, frame_id) pair. The "root" here is the
// run whose owning RunScope has `parent_run_id IS NULL` — the
// non-partition scope the fan-out parent dispatched in. Its children
// are the per-partition run-rows whose owning RunScopes carry
// `parent_run_id = <root run id>` and `partition_key = <key>`.
//
// Strategy — robust to the fan-out lifecycle:
//
//   - Project the node's most-relevant run-scope id via NodeTable.Get
//     (LATERAL picks an in-flight row when any exist; the children
//     win the ORDER BY tie under the fan-out's "parent completes
//     before children claim" lifecycle). When the projection points
//     at a partition scope, walk one hop up via
//     RunScopeTable.GetByID → ParentRunID to land on the parent's
//     run row directly. This is the path the V1 fan-out drives: the
//     parent run reaches phase=completed the moment SubClaims commit;
//     its children stay phase in {pending,active,held,parked} until
//     each terminates. The parent run-id is NOT recoverable through
//     `GetInFlightRunForNode(nodeID, parentScopeID)` (the parent's
//     run-row is no longer in-flight); we go through the scope's
//     parent_run_id pointer instead, which survives the parent's
//     terminal phase.
//
//   - When the projection points at a non-partition scope (no
//     `parent_run_id` on the scope), the parent IS the projected
//     run — call GetInFlightRunForNode to resolve it. This covers
//     the in-flight parent case (e.g. before children have claimed,
//     or a non-fan-out target).
//
// The `frameID` argument is consulted to short-circuit when the
// node's projection points at a different frame entirely (template
// churn or stale projection).
//
// Returns (nil, nil) when no projection or no parent can be resolved
// — the caller emits an empty partition list.
func findRootRunInFrame(
	ctx context.Context, deps AppDeps, tx persistence.Tx,
	nodeID shared.UUID, frameID shared.UUID,
) (*persistence.RunTreeRow, error) {
	node, err := deps.Persist.Nodes().Get(ctx, nodeID, tx)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, nil
	}
	// Frame mismatch on the node projection: the node's latest frame
	// has moved on; the backfill's frame is no longer the one the
	// node row projects. Empty partitions — the caller short-circuits.
	if node.FrameID != nil && *node.FrameID != frameID {
		return nil, nil
	}
	if node.RunScopeID == nil {
		// No in-flight run for the node — nothing to drill into.
		return nil, nil
	}
	// Look up the projected scope. If it carries a parent_run_id, the
	// projection points at a partition child's scope and the parent
	// run-id is the scope's parent_run_id directly. Otherwise the
	// scope IS the parent's (no partition keying); we resolve the
	// parent's in-flight run via the queue helper.
	scope, err := deps.Persist.RunScopes().GetByID(ctx, tx, *node.RunScopeID)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, nil
	}
	var parentRunID shared.UUID
	if scope.ParentRunID != nil {
		// Partition child's scope → parent run-id is the scope's
		// parent_run_id pointer. This survives the parent's
		// phase=completed transition.
		parentRunID = *scope.ParentRunID
	} else {
		// Non-partition scope → the projection IS the parent run.
		// Use GetInFlightRunForNode to resolve the run-id (only
		// in-flight while the parent has not yet reached terminal;
		// for a fan-out the parent terminates fast, so this branch
		// is the non-fan-out / pre-fan-out case).
		runID, ok, qerr := deps.Queue.GetInFlightRunForNode(ctx, tx, nodeID, *node.RunScopeID)
		if qerr != nil {
			return nil, qerr
		}
		if !ok {
			return nil, nil
		}
		parentRunID = runID
	}
	return deps.Persist.RunTree().GetByID(ctx, tx, parentRunID)
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
