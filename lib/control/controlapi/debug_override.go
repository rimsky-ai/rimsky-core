// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: debug-channel
// @concept: breakpoint

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	debugActionInvalidateNode = "invalidate_node"
	debugActionSetAttribute   = "set_attribute"
)

type DebugOverrideRequest struct {
	Action         string `json:"action"`
	NodeType       string `json:"node_type"`
	AttributeKey   string `json:"attribute_key,omitempty"`
	AttributeValue any    `json:"attribute_value,omitempty"`
}

var errInstanceNotDebuggable = errors.New("instance not in debuggable state")

var errDebugOverrideUnknownNodeType = errors.New("node_type does not match any node in this instance")

type debugCrossFrameRunError struct {
	NodeID        shared.UUID
	NodeType      string
	ActiveFrameID shared.UUID
	RunFrameID    shared.UUID
}

func (e *debugCrossFrameRunError) Error() string {
	return fmt.Sprintf(
		"node %s (type %q) latest run belongs to frame %s, not the active frame %s",
		e.NodeID, e.NodeType, e.RunFrameID, e.ActiveFrameID)
}

func registerDebugOverrideRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{id}/debug/override", gate(deps, "instance:debug-override", handleDebugOverride(deps)))
}

func handleDebugOverride(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		instanceID, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		var body DebugOverrideRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		switch body.Action {
		case debugActionInvalidateNode:
			if body.NodeType == "" {
				badRequest(w, "node_type is required for action=invalidate_node")
				return
			}
		case debugActionSetAttribute:
			if body.NodeType == "" {
				badRequest(w, "node_type is required for action=set_attribute")
				return
			}
			if body.AttributeKey == "" {
				badRequest(w, "attribute_key is required for action=set_attribute")
				return
			}
		case "":
			badRequest(w, "action is required (one of invalidate_node, set_attribute)")
			return
		default:
			badRequest(w, fmt.Sprintf("unknown action %q (one of invalidate_node, set_attribute)", body.Action))
			return
		}
		instUUID := shared.UUID(instanceID)
		actor := requestingKeyID(req.Context())
		isDryRun := ModeFromContext(req.Context()) == authModeDryRun

		var (
			gateState   string
			mutatedRuns int
			notFound    bool
		)
		txErr := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, instUUID, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				notFound = true
				return nil
			}
			gatePaused := inst.Paused
			gateHit := false
			if !gatePaused {
				hit, err := deps.Persist.BreakpointHits().HasUnresumedPauseHitForInstance(ctx, instUUID, tx)
				if err != nil {
					return err
				}
				gateHit = hit
			}
			if !gatePaused && !gateHit {
				return errInstanceNotDebuggable
			}
			switch {
			case gatePaused:
				gateState = "paused"
			case gateHit:
				gateState = "breakpoint"
			}
			if isDryRun {
				return errDryRunOK
			}
			n, err := applyDebugOverride(ctx, deps, tx, instUUID, body)
			if err != nil {
				return err
			}
			mutatedRuns = n
			payload := map[string]any{
				"action":          body.Action,
				"node_type":       body.NodeType,
				"gate_state":      gateState,
				"actor":           actor,
				"runs_mutated":    mutatedRuns,
				"attribute_key":   body.AttributeKey,
				"attribute_value": body.AttributeValue,
			}
			return deps.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &instUUID,
				Kind:       events.KindDebugOverrideApplied(),
				Payload:    payload,
			}, tx)
		})
		if isDryRun && errors.Is(txErr, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_applied_debug_override", map[string]any{
				"instance_id": instanceID.String(),
				"action":      body.Action,
				"node_type":   body.NodeType,
				"gate_state":  gateState,
			})
			return
		}
		if notFound {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		if errors.Is(txErr, errInstanceNotDebuggable) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":  errInstanceNotDebuggable.Error(),
				"states": []string{"paused", "breakpoint"},
			})
			return
		}
		if errors.Is(txErr, errDebugOverrideUnknownNodeType) {
			badRequest(w, txErr.Error())
			return
		}
		var crossFrameErr *debugCrossFrameRunError
		if errors.As(txErr, &crossFrameErr) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           crossFrameErr.Error(),
				"node_id":         crossFrameErr.NodeID.String(),
				"node_type":       crossFrameErr.NodeType,
				"active_frame_id": crossFrameErr.ActiveFrameID.String(),
				"run_frame_id":    crossFrameErr.RunFrameID.String(),
			})
			return
		}
		if txErr != nil {
			writeError(w, txErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"gate_state":   gateState,
			"runs_mutated": mutatedRuns,
		})
	}
}

func applyDebugOverride(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	instanceID shared.UUID,
	body DebugOverrideRequest,
) (int, error) {
	frameID, err := deps.Persist.Frames().GetRunningFrameID(ctx, instanceID, tx)
	if err != nil {
		return 0, err
	}
	nodes, err := deps.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return 0, err
	}
	matchesNodeType := false
	for _, n := range nodes {
		if n.NodeType == body.NodeType {
			matchesNodeType = true
			break
		}
	}
	if !matchesNodeType {
		return 0, fmt.Errorf("%w: %q", errDebugOverrideUnknownNodeType, body.NodeType)
	}
	mutated := 0
	for _, n := range nodes {
		if n.NodeType != body.NodeType {
			continue
		}
		inFrameLatest, err := resolveActiveFrameLatestRun(ctx, deps, tx, n, frameID)
		if err != nil {
			return mutated, err
		}
		touched := false
		switch body.Action {
		case debugActionSetAttribute:
			wrote, err := setNodeAttributeForDebugOverride(ctx, deps, tx, n, frameID, inFrameLatest, body)
			if err != nil {
				return mutated, err
			}
			touched = wrote
		case debugActionInvalidateNode:
			if inFrameLatest != nil {
				if _, err := deps.Persist.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
					NodeID:         n.ID,
					RunScopeID:     inFrameLatest.RunScopeID,
					FrameID:        *frameID,
					ExecutorName:   n.Executor,
					EnqueuedAt:     time.Now().UTC(),
					CreationReason: cascade.CreationReasonOperatorInvalidate,
				}); err != nil {
					return mutated, err
				}
				touched = true
			}
		}
		if touched {
			mutated++
		}
	}
	return mutated, nil
}

func resolveActiveFrameLatestRun(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	n persistence.NodeRow,
	frameID *shared.UUID,
) (*persistence.NodeRunLatest, error) {
	latest, err := deps.Persist.Nodes().GetLatestRunForNode(ctx, tx, n.ID)
	if err != nil {
		return nil, err
	}
	if latest == nil || frameID == nil {
		return nil, nil
	}
	if latest.FrameID != *frameID {
		return nil, &debugCrossFrameRunError{
			NodeID:        n.ID,
			NodeType:      n.NodeType,
			ActiveFrameID: *frameID,
			RunFrameID:    latest.FrameID,
		}
	}
	return latest, nil
}

func setNodeAttributeForDebugOverride(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	n persistence.NodeRow,
	frameID *shared.UUID,
	inFrameLatest *persistence.NodeRunLatest,
	body DebugOverrideRequest,
) (bool, error) {
	if inFrameLatest == nil {
		return false, nil
	}
	targetRunID := inFrameLatest.NodeRunID
	if cascade.IsTerminal(inFrameLatest.State) {
		newRunID, err := deps.Persist.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
			NodeID:         n.ID,
			RunScopeID:     inFrameLatest.RunScopeID,
			FrameID:        *frameID,
			ExecutorName:   n.Executor,
			EnqueuedAt:     time.Now().UTC(),
			CreationReason: cascade.CreationReasonOperatorInvalidate,
		})
		if err != nil {
			return false, err
		}
		targetRunID = newRunID
	}
	delta := map[string]any{body.AttributeKey: body.AttributeValue}
	existing, err := deps.Persist.NodeAttributes().GetByRun(ctx, targetRunID, tx)
	if err != nil {
		return false, err
	}
	if existing == nil {
		if err := deps.Persist.NodeAttributes().Upsert(ctx, targetRunID, n.ID, delta, tx); err != nil {
			return false, err
		}
	} else if err := deps.Persist.NodeAttributes().MergeDelta(ctx, targetRunID, delta, tx); err != nil {
		return false, err
	}
	if isDispatchedInFlight(inFrameLatest.State) {
		// @story: operator-invalidate-queues-during-flight
		if _, err := deps.Persist.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
			NodeID:         n.ID,
			RunScopeID:     inFrameLatest.RunScopeID,
			FrameID:        *frameID,
			ExecutorName:   n.Executor,
			EnqueuedAt:     time.Now().UTC(),
			CreationReason: cascade.CreationReasonOperatorInvalidate,
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func isDispatchedInFlight(state cascade.NodeState) bool {
	switch state {
	case cascade.NodeStateRunning, cascade.NodeStateHeld, cascade.NodeStateParked:
		return true
	}
	return false
}
