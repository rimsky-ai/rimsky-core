// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// debug_override.go — Pass 9 of the message-schema-layer plan.
//
// POST /instances/{id}/debug/override is the gated debug-channel
// endpoint operators reach for when an instance is paused or held at a
// pause-mode breakpoint hit and the operator needs to inject a one-off
// mutation (stale-mark a node-run, write an attribute) to get the
// graph unstuck. Distinct from `node:invalidate` and `node:reset`
// (always-on operator surfaces): this surface is gated to the
// debuggable lifecycle states only, audited under a dedicated
// `debug.override.applied` operational kind, and authorized by the
// `instance:debug-override` permission scope.
//
// Load-bearing properties:
//
//   - The gate (paused OR unresumed pause-mode breakpoint hit) is read
//     and the mutation is applied INSIDE THE SAME TX. Splitting them
//     would admit a TOCTOU window where an external pause-toggle
//     between gate-check and mutation could leave the instance in a
//     partial state. The check + the writes share one snapshot.
//   - An undeclared action returns 400 BEFORE the tx opens; the gate
//     does not get probed by a malformed request.
//   - The audit event ride the same tx as the mutation — a rollback
//     mid-tx rolls back BOTH the mutation and the audit row, never
//     audits a mutation that didn't happen.
//
// @concept: debug-channel
// @concept: breakpoint

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @agent-contract: invalidate_node is the gated debug-channel action —
// distinct from the retired operator-invalidate route
// (concept:debug-channel vs concept:message). The debug action stale-
// marks every in-flight run of the named node-type in the running
// frame; set_attribute merges a single key into the named node-type's
// latest attribute bag and stale-marks so the next cycle picks the new
// value up.
const (
	debugActionInvalidateNode = "invalidate_node"
	debugActionSetAttribute   = "set_attribute"
)

// DebugOverrideRequest is the POST body shape.
type DebugOverrideRequest struct {
	Action         string `json:"action"`
	NodeType       string `json:"node_type"`
	AttributeKey   string `json:"attribute_key,omitempty"`
	AttributeValue any    `json:"attribute_value,omitempty"`
}

// errInstanceNotDebuggable is the sentinel returned when the gate
// rejects: the instance is neither paused nor holding an unresumed
// pause-mode breakpoint hit. Mapped to HTTP 409 with both predicate
// names so the operator sees exactly which states would have unlocked
// the override.
var errInstanceNotDebuggable = errors.New("instance not in debuggable state")

// registerDebugOverrideRoutes wires the POST /instances/{id}/debug/override
// endpoint.
func registerDebugOverrideRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{id}/debug/override", gate(deps, "instance:debug-override", handleDebugOverride(deps)))
}

// handleDebugOverride applies the ad-hoc override inside the same tx as
// the gate-check (TOCTOU resistance) and emits the debug.override.applied
// audit event in the SAME tx so a rollback never leaves a half-state.
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
		// @constraint: request-level validation runs BEFORE the tx so a
		// malformed request never probes the gate; an attacker could
		// otherwise use timing differences between
		// "well-formed-but-gated" and "malformed" to fingerprint which
		// instances are paused.
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
			// @constraint: gate predicate is paused OR there is an
			// unresumed pause-mode breakpoint hit BLOCKING A RUNNER on
			// this instance. `HasUnresumedPauseHitForInstance` joins the
			// hit row to its referenced node-run and requires the
			// node-run still be in a non-terminal phase
			// (pending/active/held/parked) — a stale hit row whose
			// runner died or has already terminated is NOT a blocker
			// and the gate refuses. Both reads share the tx so a
			// concurrent pause-toggle or breakpoint-resume committing
			// between them sees one or the other snapshot, never half.
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
			// @constraint: dry-run gate fires after instance + gate
			// validation, before mutation; surface the
			// would_have_applied envelope and roll back the tx so the
			// never-mutate property holds.
			if isDryRun {
				return errDryRunOK
			}
			n, err := applyDebugOverride(ctx, deps, tx, instUUID, body)
			if err != nil {
				return err
			}
			mutatedRuns = n
			// @constraint: audit event rides the mutation's tx so a
			// rollback mid-flow never audits a mutation that didn't
			// happen and never mutates without auditing.
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
			// @deliberate: HTTP 409 body carries both predicate names so
			// the operator sees which states would have unlocked the
			// override without re-reading the spec.
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

// applyDebugOverride performs the mutation step inside the caller's tx.
// Returns the number of node-runs that were actually touched (stale-
// marked, or for set_attribute the attribute row was written). A matched
// NodeType whose in-flight run is absent AND whose attribute row has
// nowhere to land does NOT contribute to the count — the audit row and
// HTTP response would otherwise advertise phantom mutations on idle
// instances.
func applyDebugOverride(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	instanceID shared.UUID,
	body DebugOverrideRequest,
) (int, error) {
	// @constraint: stale-mark binds to the running frame's frame_id;
	// staling a run without one would strand the run with no frame to
	// carry it (concept:cascade). When no frame is running, the
	// stale-mark cannot be applied; surface that as a no-op (zero runs
	// mutated) so the operator still sees the audit row recording the
	// attempt on an idle instance.
	frameID, err := deps.Persist.Frames().GetRunningFrameID(ctx, instanceID, tx)
	if err != nil {
		return 0, err
	}
	// @deliberate: list by instance and filter in code because
	// NodeListFilter has no NodeType field yet; paging isn't necessary
	// at this scale (debug overrides target a single node-type,
	// typically a one-digit-count of matches per instance).
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
		// @constraint: set_attribute writes the attribute value to the
		// latest attribute row for (node, main_run_scope) BEFORE
		// stale-marking so the next run picks up the override.
		if body.Action == debugActionSetAttribute {
			wrote, err := setNodeAttributeForDebugOverride(ctx, deps, tx, n, body)
			if err != nil {
				return mutated, err
			}
			if wrote {
				touched = true
			}
		}
		// @constraint: both actions stale-mark the in-flight run when
		// one exists; set_attribute without a stale-mark would leave
		// the new value sitting in storage with the run unaware of it.
		if n.InFlightRunID != nil && frameID != nil {
			if err := deps.Persist.Nodes().MarkStaleForCascade(ctx, *n.InFlightRunID, *frameID, tx); err != nil {
				return mutated, err
			}
			touched = true
		}
		if touched {
			mutated++
		}
	}
	return mutated, nil
}

// setNodeAttributeForDebugOverride writes the operator-supplied
// attribute key/value into the in-flight run's attribute bag and
// merges it through the standard MergeDelta path so the persisted
// row carries the new value. The function exists for the
// set_attribute action arm and is wholly inside the caller's tx.
//
// Resolution scope: ONLY the in-flight run is targeted. A no-in-flight
// run case is treated as a no-op (returns (false, nil)) rather than
// writing to a retired run's attribute row in the main RunScope.
// Writing to a retired row was a silent two-segment behavioural mode:
// whether the next dispatch's resolver picked up the value depended
// on the (implicit) attribute-resolution preference order between
// "latest attribute row in main RunScope" vs. a freshly-allocated row
// at next dispatch, and the visibility was not pinned by any test.
// STORY-debug-channel's gate says "the override applies in that frame"
// — refusing the no-in-flight case keeps that guarantee crisp instead
// of admitting the silent mode.
//
// Returns (true, nil) when the in-flight attribute row was actually
// written; (false, nil) when there is no in-flight run (the caller
// records the attempt in the audit row but does NOT bump the mutated
// count — see applyDebugOverride's `touched` accounting).
func setNodeAttributeForDebugOverride(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	n persistence.NodeRow,
	body DebugOverrideRequest,
) (bool, error) {
	delta := map[string]any{body.AttributeKey: body.AttributeValue}
	if n.InFlightRunID == nil {
		// @constraint: refuse the write when there is no in-flight run
		// (see the docstring's resolution-scope paragraph).
		return false, nil
	}
	// @deliberate: MergeDelta writes into the active run's attribute
	// row; if the row hasn't been created yet (lazy allocation), Upsert
	// a fresh one carrying just the override.
	existing, err := deps.Persist.NodeAttributes().GetByRun(ctx, *n.InFlightRunID, tx)
	if err != nil {
		return false, err
	}
	if existing == nil {
		if err := deps.Persist.NodeAttributes().Upsert(ctx, *n.InFlightRunID, n.ID, delta, tx); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := deps.Persist.NodeAttributes().MergeDelta(ctx, *n.InFlightRunID, delta, tx); err != nil {
		return false, err
	}
	return true, nil
}
