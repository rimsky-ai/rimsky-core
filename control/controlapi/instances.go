// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instances.go — POST /instances, GET /instances, GET /instances/:id_or_key,
// DELETE /instances/:id_or_key. Includes the instance-factory logic that
// provisions instance + nodes + schedules from a template.
//
// Provisioning flow (post-control-plane v1):
//  1. Lock the template row FOR UPDATE; reject if state ≠ 'deployed'.
//     Idempotently resolve a pre-existing (template_hash, instance_key)
//     row by returning its instance_id (spec §2.2).
//  2. Validate instance_key uniqueness via InstanceTable.Create. The
//     params map is stored verbatim on rimsky_instances.params; both
//     single-brace `{params.x}` (instantiation) and double-brace
//     `{{params.x}}` (dispatch) consumers re-read this row, so there is
//     no per-instance baked node config to apply substitutions to
//     (spec §10.1).
//  3. Allocate node UUIDs up-front so dependencies[] can be rewritten
//     from node-type names to node IDs.
//  4. Create one node row per template node.
//  5. For schedule nodes, compute the next cron fire time and register.
//  6. For root nodes (no deps), enqueue the first frame so the
//     scheduler can advance them.
//  7. After commit, fire OnInstanceCreated against every store named
//     in the template's spec (spec §5.4 / §5.5).
//
// Resources / concurrency-tags from the previous shape were retired in
// the redesign (spec §11.3); their replacements (stores, locks) live
// entirely on the template and are read by the supervisor at dispatch
// time, not baked here.
package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	foundationshared "github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/frame"
	nodepkg "github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/runtime"
)

type createInstanceRequest struct {
	Template    string         `json:"template"` // tag or hash; per spec §2.2.
	InstanceKey *string        `json:"instance_key,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	// UserdataOverrides is a per-instance ad-hoc override blob deep-merged
	// into per-node userdata at dispatch time. Shape:
	//   {
	//     "by_executor": {"<executor-name>": {<userdata-fragment>}},
	//     "by_node":     {"<node-name>":     {<userdata-fragment>}}
	//   }
	// Both keys optional. Executor names validated against the operator-
	// declared executors block; node names validated against the locked
	// template's nodes. Unknown names fail with 400. Per
	// @blessed-invariant 11 the fragment values themselves are opaque to
	// rimsky — only the keys are inspected (for routing / validation).
	UserdataOverrides map[string]any `json:"userdata_overrides,omitempty"`
	// FrameDeliveryMode selects per-instance message-delivery semantics
	// for `DeliverPendingMessages` at frame creation
	// (col:rimsky_instances.frame_delivery_mode). Valid values:
	// "serial_queue" (deliver oldest pending message; the rest stay
	// pending) or "coalesce" (deliver all pending). Optional — when
	// omitted the column's default ("coalesce") is used.
	FrameDeliveryMode *string `json:"frame_delivery_mode,omitempty"`
}

type createInstanceResponse struct {
	InstanceID   string  `json:"instance_id"`
	TemplateHash string  `json:"template_hash"`
	InstanceKey  *string `json:"instance_key,omitempty"`
	NodeCount    int     `json:"node_count"`
}

type instanceItem struct {
	ID                string         `json:"id"`
	TemplateHash      string         `json:"template_hash"`
	InstanceKey       *string        `json:"instance_key,omitempty"`
	Params            map[string]any `json:"params"`
	UserdataOverrides map[string]any `json:"userdata_overrides,omitempty"`
	FrameDeliveryMode string         `json:"frame_delivery_mode"`
	CreatedAt         time.Time      `json:"created_at"`
	TerminatedAt      *time.Time     `json:"terminated_at,omitempty"`
}

func toInstanceItem(r persistence.InstanceRow, redact []string) instanceItem {
	out := instanceItem{
		ID:                r.ID.String(),
		TemplateHash:      r.TemplateHash,
		InstanceKey:       r.InstanceKey,
		Params:            ApplyParamsRedact(r.Params, redact),
		FrameDeliveryMode: r.FrameDeliveryMode,
		CreatedAt:         r.CreatedAt,
		TerminatedAt:      r.TerminatedAt,
	}
	if len(r.UserdataOverrides) > 0 {
		out.UserdataOverrides = r.UserdataOverrides
	}
	return out
}

// registerInstancesRoutes wires the /instances group.
func registerInstancesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances", gate(deps, "instance:create", handleCreateInstance(deps)))
	r.Get("/instances", gate(deps, "instance:read", handleListInstances(deps)))
	r.Get("/instances/{idOrKey}", gate(deps, "instance:read", handleGetInstance(deps)))
	r.Delete("/instances/{idOrKey}", gate(deps, "instance:terminate", handleDeleteInstance(deps)))
}

func handleCreateInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body createInstanceRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		// Empty-string instance_key is treated as absent (spec §2.2 — the
		// nullable column is the absence sentinel; an empty string would
		// participate in the unique index and break further inserts with
		// no key).
		if body.InstanceKey != nil && *body.InstanceKey == "" {
			body.InstanceKey = nil
		}
		if strings.TrimSpace(body.Template) == "" {
			badRequest(w, "template is required (tag or hash)")
			return
		}
		// Validate frame_delivery_mode early so a typo surfaces as 400
		// rather than a SQL CHECK violation deep in provisionInstanceTx.
		if body.FrameDeliveryMode != nil {
			switch *body.FrameDeliveryMode {
			case "serial_queue", "coalesce":
				// ok
			default:
				badRequest(w, fmt.Sprintf("frame_delivery_mode %q invalid (want \"serial_queue\" or \"coalesce\")", *body.FrameDeliveryMode))
				return
			}
		}
		hash, err := resolveTagOrHash(req.Context(), deps, body.Template)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, foundationshared.ErrTemplateNotFound.Error())
			return
		}
		params := body.Params
		if params == nil {
			params = map[string]any{}
		}

		// Spec §2.2 acquisition: lock the template row FOR UPDATE,
		// validate state == 'deployed', then idempotently resolve the
		// (template_hash, instance_key) collision to return the existing
		// row. Capture the locked spec for the post-commit fan-out so we
		// don't have to re-read.
		//
		// Dry-run is honored AFTER the FOR UPDATE state check + the
		// userdata_overrides validation; a dry-run create against an
		// undeployed template returns the same 409
		// `template_validation` error a real call would (per spec
		// section "Dry-run mode": "Errors from validation surface as
		// in normal flow").
		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		var (
			tplSpec           nodepkg.TemplateSpec
			respOut           createInstanceResponse
			existedKey        bool
			existingOverrides map[string]any
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			row, err := deps.Persist.Templates().LockForUpdate(ctx, hash, tx)
			if err != nil {
				return err
			}
			if row == nil {
				return foundationshared.ErrTemplateNotFound
			}
			if row.State != persistence.TemplateStateDeployed {
				return foundationshared.Wrap(foundationshared.ErrTemplateValidation,
					"instance creation requires template state 'deployed'",
					map[string]any{"template_hash": hash, "state": string(row.State)})
			}
			tplSpec = row.Spec
			// Validate userdata_overrides against the locked template's
			// node list and the operator-declared executors block. Done
			// inside the tx so that template state at the time of
			// validation matches the state of the row we'll insert
			// against.
			if vErr := validateUserdataOverrides(body.UserdataOverrides, row.Spec.Nodes, deps.Executors); vErr != nil {
				return vErr
			}
			// Dry-run gate: every validation step above has succeeded
			// (template found, state==deployed, overrides accepted).
			// Skip the mutation and signal the caller via the
			// errDryRunOK sentinel so the outer code writes the
			// synthetic envelope. The tx rolls back any FOR UPDATE
			// state and the LockForUpdate-acquired row lock.
			if isDryRun {
				return errDryRunOK
			}
			// Idempotent resolution on (template_hash, instance_key).
			if body.InstanceKey != nil {
				existing, err := deps.Persist.Instances().GetByInstanceKey(ctx, hash, *body.InstanceKey, tx)
				if err != nil {
					return err
				}
				if existing != nil {
					existedKey = true
					existingOverrides = existing.UserdataOverrides
					respOut = createInstanceResponse{
						InstanceID:   existing.ID.String(),
						TemplateHash: existing.TemplateHash,
						InstanceKey:  existing.InstanceKey,
						NodeCount:    len(row.Spec.Nodes),
					}
					return nil
				}
			}
			deliveryMode := ""
			if body.FrameDeliveryMode != nil {
				deliveryMode = *body.FrameDeliveryMode
			}
			provisioned, err := provisionInstanceTx(ctx, deps, tx, row, provisionArgs{
				InstanceKey:       body.InstanceKey,
				Params:            params,
				UserdataOverrides: body.UserdataOverrides,
				FrameDeliveryMode: deliveryMode,
			})
			if err != nil {
				return err
			}
			respOut = provisioned
			return nil
		})
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_created", map[string]any{
				"instance_id":   "dry-run-not-persisted",
				"template_hash": hash,
				"params":        params,
			})
			return
		}
		if err != nil {
			if errors.Is(err, foundationshared.ErrTemplateNotFound) {
				notFoundResp(w, foundationshared.ErrTemplateNotFound.Error())
				return
			}
			if errors.Is(err, errUserdataOverridesInvalid) {
				badRequest(w, err.Error())
				return
			}
			if errors.Is(err, foundationshared.ErrTemplateValidation) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		// Spec §2.2 / §5.4 mandate firing OnInstanceCreated on every call,
		// including idempotent re-creates: the fan-out helper is already
		// progress-preserving (skip-if-already-at-target via the
		// rimsky_lifecycle_idempotencies bookkeeping), so a partial-failure on
		// the original creation will be resumed here.
		paramsBytes, err := json.Marshal(params)
		if err != nil {
			writeError(w, err)
			return
		}
		instanceKey := ""
		if respOut.InstanceKey != nil {
			instanceKey = *respOut.InstanceKey
		}
		if _, perStore, err := FanOutInstanceEvent(req.Context(), deps,
			EventInstanceCreated, hash, respOut.InstanceID, tplSpec,
			InstancePayload{InstanceKey: instanceKey, Params: paramsBytes}, nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "instance lifecycle fan-out failed",
				"details": perStore,
			})
			return
		}
		// Walk the template's `publishers:` block and issue
		// Publisher.Subscribe for each. Per spec §Per-instance
		// parameterization, publisher startup failures are non-blocking
		// — the publisher-subscription row stays at `state = failed`
		// for operator-recoverable retries via the resync sweeper.
		if len(tplSpec.Publishers) > 0 && !existedKey {
			instUUID, parseErr := uuid.Parse(respOut.InstanceID)
			if parseErr == nil {
				_ = runtime.StartPublisherSubscriptionsForInstance(req.Context(), runtime.PublisherLifecycleDeps{
					Persist:    deps.Persist,
					Publishers: deps.Publishers,
					Clock:      deps.Clock,
					Logger:     deps.Logger,
				}, foundationshared.UUID(instUUID), params, tplSpec.Publishers)
			}
		}
		status := http.StatusCreated
		if existedKey {
			status = http.StatusOK
		}
		// Audit trail for ad-hoc per-instance overrides. Logs key names
		// only (per @blessed-invariant 11 the userdata fragments
		// themselves are opaque and could carry arbitrary data — never
		// log them). Operators can confirm via the /instances/:id GET
		// response, which echoes the full userdata_overrides verbatim.
		if !existedKey && len(body.UserdataOverrides) > 0 {
			byExecutor, byNode := overridePresentKeys(body.UserdataOverrides)
			deps.Logger.Info("instance.userdata_overrides_attached",
				"instance_id", respOut.InstanceID,
				"template_hash", respOut.TemplateHash,
				"by_executor", byExecutor,
				"by_node", byNode)
		}
		// Idempotent re-create with a non-empty overrides body: rimsky
		// returns the existing row's persisted overrides, so the
		// caller's blob would be silently dropped (mirrors how `params`
		// works on idempotent re-create). Only emit the WARN when the
		// caller's body actually differs from the persisted row —
		// otherwise an operator's reconcile loop would emit a noisy
		// "discarded" warning on every retry, even though nothing was
		// actually discarded (the values are identical).
		if existedKey && len(body.UserdataOverrides) > 0 && !overridesEqual(body.UserdataOverrides, existingOverrides) {
			byExecutor, byNode := overridePresentKeys(body.UserdataOverrides)
			deps.Logger.Warn("instance.userdata_overrides_replaced_by_idempotent_match",
				"instance_id", respOut.InstanceID,
				"template_hash", respOut.TemplateHash,
				"by_executor", byExecutor,
				"by_node", byNode)
		}
		writeJSON(w, status, respOut)
	}
}

func handleListInstances(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		filter := persistence.InstanceListFilter{
			TemplateHash: q.Get("template_hash"),
		}
		if v := q.Get("active"); v != "" {
			b := v == "1" || v == "true"
			filter.Active = &b
		}
		pag := persistence.ListPagination{
			Limit:  parseLimit(req, 100),
			Cursor: q.Get("cursor"),
		}
		var page persistence.PaginatedListResult[persistence.InstanceRow]
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Instances().List(ctx, filter, pag, tx)
			page = p
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		// Redact per-template — look up each row's template to grab its
		// params_redact slice.
		items := make([]instanceItem, 0, len(page.Rows))
		redactCache := map[string][]string{}
		for _, r := range page.Rows {
			redact, ok := redactCache[r.TemplateHash]
			if !ok {
				var tpl *persistence.TemplateRow
				if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
					t, err := deps.Persist.Templates().GetByHash(ctx, r.TemplateHash, tx)
					tpl = t
					return err
				}); err != nil && deps.Logger != nil {
					deps.Logger.Warn("handleListInstances: load template for params_redact failed; skipping redaction",
						"instance_id", r.ID.String(),
						"template_hash", r.TemplateHash,
						"error", err.Error())
				}
				if tpl != nil {
					redact = tpl.Spec.ParamsRedact
				}
				redactCache[r.TemplateHash] = redact
			}
			items = append(items, toInstanceItem(r, redact))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instances":   items,
			"next_cursor": page.NextCursor,
		})
	}
}

func handleGetInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, foundationshared.ErrInstanceNotFound.Error())
			return
		}
		var tpl *persistence.TemplateRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			t, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			tpl = t
			return err
		}); err != nil && deps.Logger != nil {
			deps.Logger.Warn("handleGetInstance: load template for params_redact failed; skipping redaction",
				"instance_id", inst.ID.String(),
				"template_hash", inst.TemplateHash,
				"error", err.Error())
		}
		var redact []string
		if tpl != nil {
			redact = tpl.Spec.ParamsRedact
		}
		writeJSON(w, http.StatusOK, toInstanceItem(*inst, redact))
	}
}

func handleDeleteInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, foundationshared.ErrInstanceNotFound.Error())
			return
		}
		// Spec §2.4: instance deletion is only sanctioned after the
		// instance has reached terminal state (terminated_at IS NOT NULL).
		// The terminator worker fires OnInstanceTerminated as soon as the
		// row terminates; a DELETE on an active instance would bypass that
		// trigger and risks firing the lifecycle event against a still-
		// running instance.
		if inst.TerminatedAt == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "instance is not in terminal state; wait for terminated_at to be set",
			})
			return
		}
		// Dry-run: validation passed; skip the fan-out + row delete.
		if WriteDryRunResponse(w, req, "would_have_terminated", map[string]any{
			"instance_id": inst.ID.String(),
		}) {
			return
		}
		// Spec §1.6 / §5.5: fire OnInstanceTerminated to every store
		// referenced by the instance's template before deleting the row.
		// FanOutInstanceEvent deletes per-store lifecycle rows on
		// success and surfaces partial failures so the operator can
		// retry; surviving lifecycle rows are picked up by the
		// terminator if the instance row remains. We only proceed with
		// the row delete after fan-out fully succeeds.
		var tpl *persistence.TemplateRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			t, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			tpl = t
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if tpl != nil {
			var terminatedAtMs int64
			if inst.TerminatedAt != nil {
				terminatedAtMs = inst.TerminatedAt.UnixMilli()
			}
			if _, perStore, err := FanOutInstanceEvent(req.Context(), deps,
				EventInstanceTerminated, inst.TemplateHash, inst.ID.String(), tpl.Spec,
				InstancePayload{TerminatedAtUnixMs: terminatedAtMs}, nil); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":   "instance lifecycle fan-out failed",
					"details": perStore,
				})
				return
			}
		} else {
			// Template gone (e.g. force-deleted): fall back to fanning
			// out via the recorded lifecycle rows so any per-instance
			// state in stores is still settled before we drop the row.
			var terminatedAtMs int64
			if inst.TerminatedAt != nil {
				terminatedAtMs = inst.TerminatedAt.UnixMilli()
			}
			if err := fanOutInstanceTerminatedFromLifecycleRows(req.Context(), deps,
				inst.TemplateHash, inst.ID.String(), terminatedAtMs); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error": err.Error(),
				})
				return
			}
		}
		// Walk active publisher-subscriptions for this instance and call
		// `Publisher.Unsubscribe` on each before dropping rows.
		// Non-blocking per spec §Per-instance parameterization —
		// failures are logged + retried by the resync sweeper.
		_ = runtime.StopPublisherSubscriptionsForInstance(req.Context(), runtime.PublisherLifecycleDeps{
			Persist:    deps.Persist,
			Publishers: deps.Publishers,
			Clock:      deps.Clock,
			Logger:     deps.Logger,
		}, inst.ID)
		// E9: walk held-durable claim_handles for this instance and call
		// `ClaimProducer.Release` on each before dropping rows. Per
		// `@blessed-invariant 22` durable claim handles persist past
		// auto-terminal; the only sanctioned release paths are the
		// operator-driven asset delete (`DELETE /instances/{id}/assets/{alias}`)
		// and instance-termination cleanup. Without this, an instance
		// with held-durable assets would leave producer-side state
		// dangling forever. Failures are logged + retained on the row
		// for retry rather than blocking instance deletion. Spec
		// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
		// §Held-durable claim lifecycle.
		if deps.Stores != nil {
			if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
				_, rErr := runtime.ReleaseHeldDurableClaims(ctx,
					runtime.RunArgs{
						Persist:       deps.Persist,
						ClaimHandles:  deps.Persist.ClaimHandles(),
						StoreRegistry: deps.Stores,
						Clock:         deps.Clock,
						Logger:        deps.Logger,
					}, tx, inst.ID, deps.Logger)
				return rErr
			}); err != nil && deps.Logger != nil {
				deps.Logger.Warn("handleDeleteInstance: ReleaseHeldDurableClaims failed",
					"instance_id", inst.ID.String(),
					"error", err.Error())
			}
		}
		// Defensive: per spec §1.6 any remaining lifecycle rows for
		// scope='instance' on this id are deleted with the instance.
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if err := deps.Persist.LifecycleIdempotency().DeleteByScope(ctx,
				persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), tx); err != nil {
				return err
			}
			return deps.Persist.Instances().Delete(ctx, inst.ID, tx)
		}); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

// fanOutInstanceTerminatedFromLifecycleRows fires OnInstanceTerminated
// against the stores recorded in rimsky_lifecycle_idempotencies for the given
// instance, deleting each row on success. Used as a fallback path when
// the bound template row is gone (e.g. force-deleted) and we cannot
// recover the spec's referenced-stores list.
//
// Unknown-store rows (the lifecycle row references a store no longer
// configured on this process) are skipped with a warning and the row is
// deleted regardless — the alternative is to wedge instance deletion
// permanently on a configuration drift the operator may have intended.
// Mirrors the terminator's fanOutFromLifecycleRows behavior so the two
// callers stay consistent.
func fanOutInstanceTerminatedFromLifecycleRows(
	ctx context.Context,
	deps AppDeps,
	templateHash, instanceID string,
	terminatedAtUnixMs int64,
) error {
	var rows []persistence.LifecycleIdempotencyRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.LifecycleIdempotency().ListByScope(ctx,
			persistence.LifecycleIdempotencyScopeInstance, instanceID, tx)
		rows = r
		return err
	}); err != nil {
		return fmt.Errorf("lifecycle row list: %w", err)
	}
	if deps.LifecycleSubs == nil {
		return errors.New("lifecycle subscriber registry not initialized")
	}
	for _, r := range rows {
		s, ok := deps.LifecycleSubs.Get(r.StoreRegistrationName)
		if !ok {
			deps.Logger.Warn("instance_delete.unknown_lifecycle_subscriber",
				"instance_id", instanceID,
				"template_hash", templateHash,
				"peer_name", r.StoreRegistrationName)
			if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return deps.Persist.LifecycleIdempotency().Delete(ctx,
					r.StoreRegistrationName,
					persistence.LifecycleIdempotencyScopeInstance,
					instanceID, tx)
			}); err != nil {
				return fmt.Errorf("delete lifecycle row %q: %w", r.StoreRegistrationName, err)
			}
			continue
		}
		if err := s.OnInstanceTerminated(ctx, locks.OnInstanceTerminatedRequest{
			InstanceID:         instanceID,
			TemplateHash:       templateHash,
			TerminatedAtUnixMs: terminatedAtUnixMs,
		}); err != nil {
			return fmt.Errorf("peer %q OnInstanceTerminated: %w", r.StoreRegistrationName, err)
		}
		if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.LifecycleIdempotency().Delete(ctx,
				r.StoreRegistrationName,
				persistence.LifecycleIdempotencyScopeInstance,
				instanceID, tx)
		}); err != nil {
			return fmt.Errorf("delete lifecycle row %q: %w", r.StoreRegistrationName, err)
		}
	}
	return nil
}

// provisionArgs carries the per-row inputs `provisionInstanceTx`
// needs from the request body. Struct-shaped (rather than positional)
// so the function signature stays narrow as new per-instance fields are
// added — cold-read style discourages 5+ positional args.
type provisionArgs struct {
	InstanceKey       *string
	Params            map[string]any
	UserdataOverrides map[string]any
	// FrameDeliveryMode is one of "serial_queue" / "coalesce". Empty
	// string falls through to the column DEFAULT 'coalesce'.
	FrameDeliveryMode string
}

// provisionInstanceTx is the instance-factory routine. Runs the create
// sequence inside the supplied tx (the same tx that locked the template
// row FOR UPDATE per spec §2.2) so the entire instance + nodes +
// schedules + initial frames are atomic with the deployed-state check.
//
// Per the stores redesign:
//   - rimsky_instances.params is stored verbatim. Both `{params.x}`
//     (instantiation, single-brace) and `{{params.x}}` (dispatch,
//     double-brace) consumers re-read this row, so there is no
//     instantiation-time substitution to apply here (spec §10.1).
//   - Concurrency tags and owned/read resources are gone (spec §11.3);
//     stores/locks live on the template and are resolved at dispatch.
func provisionInstanceTx(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	tpl *persistence.TemplateRow,
	args provisionArgs,
) (createInstanceResponse, error) {
	// Create instance row (fails with ErrInstanceKeyConflict if duplicate).
	inst, err := deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:                uuid.New(),
		TemplateHash:      tpl.ID,
		InstanceKey:       args.InstanceKey,
		Params:            args.Params,
		UserdataOverrides: args.UserdataOverrides,
		FrameDeliveryMode: args.FrameDeliveryMode,
	}, tx)
	if err != nil {
		return createInstanceResponse{}, err
	}

	// Allocate one UUID per node up-front so dependencies[] can be rewritten.
	nodeIDs := make(map[string]foundationshared.UUID, len(tpl.Spec.Nodes))
	for _, def := range tpl.Spec.Nodes {
		nodeIDs[def.Type] = uuid.New()
	}

	// Phase 1: create nodes (Create defaults to 'fresh' per the baseline
	// schema + spec §3.1) + register schedules. Phase 2 enqueues an initial frame
	// for each root.
	//
	// Post-2026-05-14: cascade-coupling is declared receiver-side via
	// `subscribes:`; the per-template subscription-edge inverse map
	// (graph/node/subscription_edges.go) drives cascade walks. The
	// retired `dependencies` column is no longer populated.
	for _, def := range tpl.Spec.Nodes {
		nodeID := nodeIDs[def.Type]
		// Subscription validity is checked at template-deploy time by
		// the validator; we still emit an instance-time error on
		// missing target so a hand-rolled spec doesn't silently
		// dispatch.
		for _, s := range def.Subscribes {
			if s.Node == "" {
				continue
			}
			if _, ok := nodeIDs[s.Node]; !ok {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: subscribe references unknown node %q (on node %q)", s.Node, def.Type)
			}
		}

		// Create node row. Per the 2026-05-15 data-platform-extensions
		// plan D7/E16/B10 the per-node `schedule:` field and the
		// rimsky_schedules table are retired; the bundled `sensor-cron`
		// service owns cron firing via the Sensor protocol.
		if _, err := deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: inst.ID,
			NodeType:   def.Type,
			Executor:   def.Executor,
		}, tx); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create node %q: %w", def.Type, err)
		}
	}

	// Phase 2: enqueue an initial frame for each root node (no upstream
	// subscriptions), reusing the caller's tx so the frame inserts are
	// atomic with the instance+node creation above.
	//
	// Post-2026-05-14: a "root" is a node with no `subscribes:` entries
	// naming an upstream node AND no substitution refs in its attribute
	// schema. Cross-cutting (`instance:true`) entries don't disqualify
	// a root because they fire on cascade-walks, not at instance create.
	for _, def := range tpl.Spec.Nodes {
		hasUpstream := false
		for _, s := range def.Subscribes {
			if s.Node != "" && s.Node != def.Type {
				hasUpstream = true
				break
			}
		}
		if !hasUpstream {
			for _, ref := range nodepkg.UpstreamNodeTypesFromAttributes(def) {
				if ref != def.Type {
					hasUpstream = true
					break
				}
			}
		}
		if hasUpstream {
			continue
		}
		nodeID := nodeIDs[def.Type]
		if _, err := frame.EnqueueOrCoalesce(ctx, deps.Persist, tx, inst.ID, nodeID); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: enqueue root node %q: %w", def.Type, err)
		}
	}

	return createInstanceResponse{
		InstanceID:   inst.ID.String(),
		TemplateHash: inst.TemplateHash,
		InstanceKey:  inst.InstanceKey,
		NodeCount:    len(tpl.Spec.Nodes),
	}, nil
}
