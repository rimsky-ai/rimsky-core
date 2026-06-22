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
	mutated := 0
	for _, n := range nodes {
		if n.NodeType != body.NodeType {
			continue
		}
		touched := false
		if body.Action == debugActionSetAttribute {
			wrote, err := setNodeAttributeForDebugOverride(ctx, deps, tx, n, body)
			if err != nil {
				return mutated, err
			}
			if wrote {
				touched = true
			}
		}
		if frameID != nil {
			latest, err := deps.Persist.Nodes().GetLatestRunForNode(ctx, tx, n.ID)
			if err != nil {
				return mutated, err
			}
			if latest != nil {
				_, err := deps.Persist.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
					NodeID:         n.ID,
					RunScopeID:     latest.RunScopeID,
					FrameID:        *frameID,
					ExecutorName:   n.Executor,
					EnqueuedAt:     time.Now().UTC(),
					CreationReason: cascade.CreationReasonOperatorInvalidate,
				})
				if err != nil {
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

func setNodeAttributeForDebugOverride(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	n persistence.NodeRow,
	body DebugOverrideRequest,
) (bool, error) {
	delta := map[string]any{body.AttributeKey: body.AttributeValue}
	latest, err := deps.Persist.Nodes().GetLatestRunForNode(ctx, tx, n.ID)
	if err != nil {
		return false, err
	}
	if latest == nil {
		return false, nil
	}
	existing, err := deps.Persist.NodeAttributes().GetByRun(ctx, latest.RunID, tx)
	if err != nil {
		return false, err
	}
	if existing == nil {
		if err := deps.Persist.NodeAttributes().Upsert(ctx, latest.RunID, n.ID, delta, tx); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := deps.Persist.NodeAttributes().MergeDelta(ctx, latest.RunID, delta, tx); err != nil {
		return false, err
	}
	return true, nil
}
