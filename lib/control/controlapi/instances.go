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
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// resolveNodeTags resolves a node's tag strings against the instance's
// params, returning the resolved tag list or a typed error citing which
// tag / which directive failed. Per spec
// Composes with Item 3's whole-directive value lift:
//
//   - Embedded mode (`"domain:{{params.domain}}"`) — stringify-and-concat.
//   - Whole-directive mode (`"{{params.region}}"`) — lift via
//     SubstituteValue; the lifted JSON value MUST be a string. Non-string
//     lifts fail materialization with a typed error citing the tag and
//     the resolved Go type.
//
// @concept: node
func resolveNodeTags(rawTags []string, paramsBytes json.RawMessage) ([]string, error) {
	if len(rawTags) == 0 {
		return nil, nil
	}
	ctx := attributes.ResolveContext{Params: paramsBytes}
	out := make([]string, 0, len(rawTags))
	for _, raw := range rawTags {
		val, err := attributes.SubstituteValue(raw, ctx)
		if err != nil {
			return nil, fmt.Errorf("tag %q: %w", raw, err)
		}
		switch v := val.(type) {
		case string:
			out = append(out, v)
		default:
			return nil, fmt.Errorf("tag %q resolved to non-string value (Go type %T); tag values must be strings", raw, val)
		}
	}
	return out, nil
}

type createInstanceRequest struct {
	// @constraint: template is a tag or hash.
	Template    string         `json:"template"`
	InstanceKey *string        `json:"instance_key,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	// AttributeOverrides is a per-instance ad-hoc override blob deep-merged
	// into per-node attributes at dispatch time. Shape:
	//   {
	//     "by_executor": {"<executor-name>": {<attribute-fragment>}},
	//     "by_node":     {"<node-name>":     {<attribute-fragment>}}
	//   }
	// Both keys optional. Executor names validated against the operator-
	// declared executors block; node names validated against the locked
	// template's nodes. Unknown names fail with 400. Per
	// concept:inertness the fragment values themselves are inert to
	// rimsky — only the keys are inspected (for routing / validation).
	AttributeOverrides map[string]any `json:"attribute_overrides,omitempty"`
	// FrameDeliveryMode selects per-instance message-delivery semantics
	// for `DeliverPendingMessages` at frame creation
	// (col:rimsky_instances.frame_delivery_mode). Valid values:
	// "serial_queue" (deliver oldest pending message; the rest stay
	// pending) or "coalesce" (deliver all pending). Optional — when
	// omitted the column's default ("coalesce") is used.
	FrameDeliveryMode *string `json:"frame_delivery_mode,omitempty"`
	// Paused is the create-time hold flag. When true, the instance is
	// created with rimsky_instances.paused = true; the supervisor's
	// candidate-selection skips it until POST /instances/{id}/resume
	// releases the hold. Per concept:breakpoint. Idempotent re-create
	// (same template_hash + instance_key) ignores the flag — the
	// existing row's paused value is unchanged. Operators wanting to
	// pause an existing instance call POST /instances/{id}/pause.
	Paused bool `json:"paused,omitempty"`
	// TerminateAfterRun is the create-time opt-in self-termination flag.
	// Instances are durable by default; when true, the instance is created
	// with rimsky_instances.terminate_after_run = true and self-terminates
	// after its next frame ends (strict "run at most once more" semantics).
	// Per concept:instance. Idempotent re-create (same template_hash +
	// instance_key) ignores the flag — the existing row's value is unchanged,
	// exactly as Paused behaves.
	TerminateAfterRun bool `json:"terminate_after_run,omitempty"`
	// ServiceBindings carries the per-instance late-bound service catalog.
	// Opaque JSON; shape per spec (`{<name>: {"path": "<binary-path>"}}`).
	ServiceBindings json.RawMessage `json:"service_bindings,omitempty"`
}

type createInstanceResponse struct {
	InstanceID   string  `json:"instance_id"`
	TemplateHash string  `json:"template_hash"`
	InstanceKey  *string `json:"instance_key,omitempty"`
	NodeCount    int     `json:"node_count"`
}

type instanceItem struct {
	ID                            string         `json:"id"`
	TemplateHash                  string         `json:"template_hash"`
	InstanceKey                   *string        `json:"instance_key,omitempty"`
	Params                        map[string]any `json:"params"`
	AttributeOverrides            map[string]any `json:"attribute_overrides,omitempty"`
	AttributeOverridesMatchCounts []int64        `json:"attribute_overrides_match_counts,omitempty"`
	FrameDeliveryMode             string         `json:"frame_delivery_mode"`
	Paused                        bool           `json:"paused"`
	TerminateAfterRun             bool           `json:"terminate_after_run"`
	CreatedAt                     time.Time      `json:"created_at"`
	TerminatedAt                  *time.Time     `json:"terminated_at,omitempty"`
	// ServiceBindings and CreatedByAPIKeyID surface the per-instance
	// late-bound service catalog and owning api-key so the host-agent-proxy
	// can populate its binding cache on a GET /instances/{id} cache-miss
	// fallback (when it did not observe the OnInstanceCreated lifecycle
	// event). Omitted when empty/absent — most instances carry neither.
	// Per spec 2026-05-24-host-agent-and-proxy-design.md.
	ServiceBindings   json.RawMessage `json:"service_bindings,omitempty"`
	CreatedByAPIKeyID string          `json:"created_by_api_key_id,omitempty"`
	// Subscriptions surfaces the per-subscription publisher lifecycle
	// (mounting → active, or failed with a reason) so an operator can
	// observe mounting progress from the instance instead of inferring
	// it from instance creation succeeding. Populated on the detail GET
	// only (the list endpoint stays one-query cheap).
	Subscriptions []instanceSubscriptionItem `json:"subscriptions,omitempty"`
}

// instanceSubscriptionItem is one rimsky_publisher_subscriptions row on
// the instance-detail response.
type instanceSubscriptionItem struct {
	ID            string    `json:"id"`
	PublisherName string    `json:"publisher_name"`
	Kind          string    `json:"kind"`
	State         string    `json:"state"`
	StartedAt     time.Time `json:"started_at"`
	FailureReason string    `json:"failure_reason,omitempty"`
}

func toInstanceItem(r persistence.InstanceRow, redact []string) instanceItem {
	out := instanceItem{
		ID:                r.ID.String(),
		TemplateHash:      r.TemplateHash,
		InstanceKey:       r.InstanceKey,
		Params:            ApplyParamsRedact(r.Params, redact),
		FrameDeliveryMode: r.FrameDeliveryMode,
		Paused:            r.Paused,
		TerminateAfterRun: r.TerminateAfterRun,
		CreatedAt:         r.CreatedAt,
		TerminatedAt:      r.TerminatedAt,
	}
	if len(r.AttributeOverrides) > 0 {
		out.AttributeOverrides = r.AttributeOverrides
	}
	if len(r.AttributeOverridesMatchCounts) > 0 {
		out.AttributeOverridesMatchCounts = r.AttributeOverridesMatchCounts
	}
	if len(r.ServiceBindings) > 0 {
		out.ServiceBindings = r.ServiceBindings
	}
	if r.CreatedByAPIKeyID != nil {
		out.CreatedByAPIKeyID = r.CreatedByAPIKeyID.String()
	}
	return out
}

// registerInstancesRoutes wires the /instances group.
func registerInstancesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances", gate(deps, "instance:create", handleCreateInstance(deps)))
	r.Get("/instances", gate(deps, "instance:read", handleListInstances(deps)))
	r.Get("/instances/{idOrKey}", gate(deps, "instance:read", handleGetInstance(deps)))
	r.Delete("/instances/{idOrKey}", gate(deps, "instance:terminate", handleDeleteInstance(deps)))
	r.Post("/instances/{idOrKey}/pause", gate(deps, "instance:pause", handlePauseInstance(deps)))
	r.Post("/instances/{idOrKey}/resume", gate(deps, "instance:resume", handleResumeInstance(deps)))
	r.Post("/instances/{idOrKey}/terminate", gate(deps, "instance:kill", handleTerminateInstance(deps)))
}

// handlePauseInstance toggles rimsky_instances.paused to TRUE. Per
// concept:breakpoint and spec §5.1. Returns 409 ErrInstanceAlreadyPaused
// when the row is already paused (idempotency surface — the operator's
// reconcile loop sees a stable error rather than a silent no-op).
func handlePauseInstance(deps AppDeps) http.HandlerFunc {
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
		// @concept: dry-run — the envelope is written BEFORE any state
		// change so a dry_run request never flips the paused flag (the
		// dry-run never-mutates property).
		if WriteDryRunResponse(w, req, "would_have_paused", map[string]any{
			"instance_id": inst.ID.String(),
		}) {
			return
		}
		var prior bool
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			var err error
			prior, err = deps.Persist.Instances().SetPaused(ctx, inst.ID, true, tx)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if prior {
			writeError(w, foundationshared.ErrInstanceAlreadyPaused)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"paused": true})
	}
}

// handleResumeInstance toggles rimsky_instances.paused to FALSE. Per
// concept:breakpoint and spec §5.1. Returns 409 ErrInstanceNotPaused when
// the row is already unpaused.
func handleResumeInstance(deps AppDeps) http.HandlerFunc {
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
		// @concept: dry-run — the envelope is written BEFORE any state
		// change so a dry_run request never flips the paused flag (the
		// dry-run never-mutates property).
		if WriteDryRunResponse(w, req, "would_have_resumed", map[string]any{
			"instance_id": inst.ID.String(),
		}) {
			return
		}
		var prior bool
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			var err error
			prior, err = deps.Persist.Instances().SetPaused(ctx, inst.ID, false, tx)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		if !prior {
			writeError(w, foundationshared.ErrInstanceNotPaused)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"resumed": true})
	}
}

func handleCreateInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body createInstanceRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		// @constraint: capture the authenticated identity at handler
		// entry. ident.KeyID is *shared.UUID (nil under anonymous-mode);
		// it is the created_by_api_key_id column value and the
		// OwnerAPIKeyID on the instance-created lifecycle fan-out. In
		// scope at both the provisionArgs construction and the
		// FanOutInstanceEvent payload.
		ident, _ := IdentityFromContextOK(req.Context())
		// @constraint: empty-string instance_key is treated as absent —
		// the nullable column is the absence sentinel; an empty string
		// would participate in the unique index and break further
		// inserts with no key.
		if body.InstanceKey != nil && *body.InstanceKey == "" {
			body.InstanceKey = nil
		}
		// @constraint: server-side reservation of the compose:
		// instance_key prefix — placed ahead of the template lookup and
		// any persistence write so a rejected create persists nothing.
		// Only the privileged compose path (which stamps the trusted
		// compose-origin marker) may create reserved-prefix instance
		// keys.
		if body.InstanceKey != nil && strings.HasPrefix(*body.InstanceKey, composeReservedPrefix) && !isComposeOrigin(req) {
			badRequest(w, "instance_key uses reserved prefix \"compose:\" (managed by the compose command)")
			return
		}
		if strings.TrimSpace(body.Template) == "" {
			badRequest(w, "template is required (tag or hash)")
			return
		}
		// @constraint: validate frame_delivery_mode early so a typo
		// surfaces as 400 rather than a SQL CHECK violation deep in
		// provisionInstanceTx.
		if body.FrameDeliveryMode != nil {
			switch *body.FrameDeliveryMode {
			case "serial_queue", "coalesce":
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

		// @constraint: lock the template row FOR UPDATE, validate state
		// == 'deployed', then idempotently resolve the (template_hash,
		// instance_key) collision to return the existing row. Capture
		// the locked spec for the post-commit fan-out so we don't have
		// to re-read. Dry-run is honored AFTER the FOR UPDATE state
		// check + the attribute_overrides validation; a dry-run create
		// against an undeployed template returns the same 409
		// `template_validation` error a real call would.
		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		var (
			tplSpec           nodepkg.TemplateSpec
			respOut           createInstanceResponse
			existedKey        bool
			existingOverrides map[string]any
			// fanOutBindings is the service-binding catalog the
			// OnInstanceCreated fan-out carries. It must reflect the value
			// actually persisted on the instance row — on an idempotent
			// re-create that is the existing row's bindings, which may differ
			// from this request's body.ServiceBindings.
			fanOutBindings json.RawMessage
			// fanOutOwner is the owner-api-key the OnInstanceCreated fan-out
			// carries. Like fanOutBindings it must reflect the persisted row,
			// not the current request's identity: on an idempotent re-create
			// by a different api-key the persisted owner (the original
			// creator) is what the proxy must route dispatches to — stamping
			// the re-creator's key here would poison the proxy's owner cache.
			fanOutOwner *foundationshared.UUID
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
			// @constraint: validate attribute_overrides against the
			// locked template's node list and the operator-declared
			// executors block — done inside the tx so that template
			// state at the time of validation matches the state of the
			// row we'll insert against.
			if vErr := validateAttributeOverrides(body.AttributeOverrides, row.Spec.Nodes, row.Spec.Graphs, deps.Executors); vErr != nil {
				return vErr
			}
			// @constraint: mandatory instantiation-time static-config
			// validation gate. Runs inside the FOR UPDATE tx, after the
			// override-shape check and BEFORE the dry-run gate (so a
			// dry-run create surfaces the rejection too) and before any
			// insert (so a rejected create persists nothing). The gate
			// is mandatory regardless of the registration-time
			// reference-validation mode: whatever a relaxed mode
			// (`available`/`none`) skipped at registration is enforced
			// here, where the template is deployed and all referenced
			// executors exist + have handshaked. row.Spec.Nodes is the
			// canonicalized flat node list (graphs flattened at
			// registration), so this covers both template shapes. Only
			// the statically-knowable subset is validated —
			// substitution-sourced values stay dispatch-validated
			// (@blessed-invariant 12).
			if sErr := validateStaticConfigAgainstExecutorSchemas(row.Spec.Nodes, row.Spec.Defaults, deps.ExecutorCapabilities); sErr != nil {
				return sErr
			}
			// @constraint: dry-run gate — every validation step above
			// has succeeded; skip the mutation and signal the caller via
			// the errDryRunOK sentinel so the outer code writes the
			// synthetic envelope. The tx rolls back any FOR UPDATE state
			// and the LockForUpdate-acquired row lock.
			if isDryRun {
				return errDryRunOK
			}
			// @constraint: idempotent resolution on (template_hash,
			// instance_key).
			if body.InstanceKey != nil {
				existing, err := deps.Persist.Instances().GetByInstanceKey(ctx, hash, *body.InstanceKey, tx)
				if err != nil {
					return err
				}
				if existing != nil {
					existedKey = true
					existingOverrides = existing.AttributeOverrides
					// @constraint: fan out the persisted row's bindings +
					// owner, not this request's body/identity — an
					// idempotent re-create must not rewrite the proxy's
					// binding cache or owner-routing with a divergent
					// body or a different re-creator key.
					fanOutBindings = existing.ServiceBindings
					fanOutOwner = existing.CreatedByAPIKeyID
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
			// @constraint: per-entry by_match counter array initialised at
			// length matching the request's by_match list so dispatch-time
			// increments find an indexed slot per entry. Per spec §"Persistence".
			var initialMatchCounts []int64
			if raw, ok := body.AttributeOverrides["by_match"]; ok {
				if list, ok := raw.([]any); ok {
					initialMatchCounts = make([]int64, len(list))
				}
			}
			provisioned, err := provisionInstanceTx(ctx, deps, tx, row, provisionArgs{
				InstanceKey:                   body.InstanceKey,
				Params:                        params,
				AttributeOverrides:            body.AttributeOverrides,
				AttributeOverridesMatchCounts: initialMatchCounts,
				FrameDeliveryMode:             deliveryMode,
				Paused:                        body.Paused,
				TerminateAfterRun:             body.TerminateAfterRun,
				ServiceBindings:               body.ServiceBindings,
				CreatedByAPIKeyID:             ident.KeyID,
			})
			if err != nil {
				return err
			}
			// @constraint: insert the publisher-subscription rows in the
			// SAME tx as the instance row — they commit atomically, so a
			// failure in any post-commit step of this handler (e.g. the
			// lifecycle fan-out 500ing the request) can never strand a
			// live instance with zero subscription rows. The retried
			// create resolves idempotently on (template_hash,
			// instance_key) and the rows already exist. No publisher RPC
			// happens here (the inserts are pure DB writes —
			// instance-create never blocks on publisher reachability);
			// the reconciliation worker drives the Subscribe handshake
			// asynchronously, and only the non-retryable classes
			// (unknown publisher name, config-resolve failure) insert a
			// row straight in `failed` with a reason.
			if len(row.Spec.Publishers) > 0 {
				instUUID, parseErr := uuid.Parse(provisioned.InstanceID)
				if parseErr != nil {
					// @deliberate: defensive-unreachable
					// (provisionInstanceTx stringifies a uuid.UUID) — but
					// never silent: fail the create tx so no instance
					// commits without its subscription rows.
					deps.Logger.Error("instance.publisher_subscriptions.instance_id_parse_failed",
						"instance_id", provisioned.InstanceID,
						"error", parseErr.Error())
					return fmt.Errorf("parse provisioned instance id %q: %w", provisioned.InstanceID, parseErr)
				}
				if subErr := runtime.StartPublisherSubscriptionsForInstance(ctx, runtime.PublisherLifecycleDeps{
					Persist:    deps.Persist,
					Publishers: deps.Publishers,
					Clock:      deps.Clock,
					Logger:     deps.Logger,
				}, tx, foundationshared.UUID(instUUID), params, row.Spec.Publishers); subErr != nil {
					deps.Logger.Error("instance.publisher_subscriptions.insert_failed",
						"instance_id", provisioned.InstanceID,
						"template_hash", hash,
						"error", subErr.Error())
					return fmt.Errorf("insert publisher-subscription rows for instance %s: %w", provisioned.InstanceID, subErr)
				}
			}
			// @constraint: new-create path — the persisted bindings +
			// owner are exactly what we passed to provisionInstanceTx.
			fanOutBindings = body.ServiceBindings
			fanOutOwner = ident.KeyID
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
			if errors.Is(err, errAttributeOverridesInvalid) {
				badRequest(w, err.Error())
				return
			}
			// @constraint: static-config gate rejection must surface as 400
			// with a structured validation_errors body, checked BEFORE the
			// generic ErrTemplateValidation branch below — a
			// *staticConfigGateError is-a ErrTemplateValidation by typed-error
			// semantics but must NOT take the 409 path that the conflict branch
			// assigns.
			var gateErr *staticConfigGateError
			if errors.As(err, &gateErr) {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error":             foundationshared.ErrTemplateValidation.Error(),
					"validation_errors": []map[string]string{gateErr.validationErrorEntry()},
				})
				return
			}
			if errors.Is(err, foundationshared.ErrTemplateValidation) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		// @constraint: fire OnInstanceCreated on every call, including
		// idempotent re-creates — the fan-out helper is already
		// progress-preserving (skip-if-already-at-target via the
		// rimsky_lifecycle_idempotencies bookkeeping), so a
		// partial-failure on the original creation will be resumed here.
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
			InstancePayload{
				InstanceKey:     instanceKey,
				Params:          paramsBytes,
				ServiceBindings: fanOutBindings,
				OwnerAPIKeyID:   fanOutOwner,
			}, nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "instance lifecycle fan-out failed",
				"details": perStore,
			})
			return
		}
		status := http.StatusCreated
		if existedKey {
			status = http.StatusOK
		}
		// @constraint: audit trail for ad-hoc per-instance overrides —
		// logs key names only. Under concept:inertness the attribute
		// fragments themselves are inert and could carry arbitrary data,
		// so never log them. Operators can confirm via the
		// /instances/:id GET response, which echoes the full
		// attribute_overrides verbatim.
		if !existedKey && len(body.AttributeOverrides) > 0 {
			byExecutor, byNode, byMatchCount := overridePresentKeys(body.AttributeOverrides)
			deps.Logger.Info("instance.attribute_overrides_attached",
				"instance_id", respOut.InstanceID,
				"template_hash", respOut.TemplateHash,
				"by_executor", byExecutor,
				"by_node", byNode,
				"by_match_count", byMatchCount)
		}
		// @constraint: idempotent re-create with a non-empty overrides
		// body — rimsky returns the existing row's persisted overrides,
		// so the caller's blob would be silently dropped (mirrors how
		// `params` works on idempotent re-create). Only emit the WARN
		// when the caller's body actually differs from the persisted
		// row — otherwise an operator's reconcile loop would emit a
		// noisy "discarded" warning on every retry, even though nothing
		// was actually discarded (the values are identical).
		if existedKey && len(body.AttributeOverrides) > 0 && !overridesEqual(body.AttributeOverrides, existingOverrides) {
			byExecutor, byNode, byMatchCount := overridePresentKeys(body.AttributeOverrides)
			deps.Logger.Warn("instance.attribute_overrides_replaced_by_idempotent_match",
				"instance_id", respOut.InstanceID,
				"template_hash", respOut.TemplateHash,
				"by_executor", byExecutor,
				"by_node", byNode,
				"by_match_count", byMatchCount)
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
		// @constraint: redact per-template — look up each row's template
		// to grab its params_redact slice.
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

// instanceRedact loads the bound template's ParamsRedact list for an
// instance projection. A failed template load is non-fatal: it WARN-logs
// and returns nil (no redaction) rather than failing the read — the same
// best-effort discipline handleListInstances uses per-hash. Used by the
// single-instance projection handlers (GET, terminate).
func instanceRedact(ctx context.Context, deps AppDeps, templateHash string, instanceID foundationshared.UUID) []string {
	var tpl *persistence.TemplateRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		t, err := deps.Persist.Templates().GetByHash(ctx, templateHash, tx)
		tpl = t
		return err
	}); err != nil && deps.Logger != nil {
		deps.Logger.Warn("instanceRedact: load template for params_redact failed; skipping redaction",
			"instance_id", instanceID.String(),
			"template_hash", templateHash,
			"error", err.Error())
	}
	if tpl != nil {
		return tpl.Spec.ParamsRedact
	}
	return nil
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
		redact := instanceRedact(req.Context(), deps, inst.TemplateHash, inst.ID)
		item := toInstanceItem(*inst, redact)
		// @concept: publisher-subscription — per-subscription publisher
		// state, the operator-visible mounting → active lifecycle, with
		// the failure reason for non-retryable failed rows.
		subs, err := deps.Persist.PublisherSubscriptions().ListByInstance(req.Context(), inst.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		for _, s := range subs {
			item.Subscriptions = append(item.Subscriptions, instanceSubscriptionItem{
				ID:            s.ID.String(),
				PublisherName: s.PublisherName,
				Kind:          s.Kind,
				State:         s.State,
				StartedAt:     s.StartedAt,
				FailureReason: s.FailureReason,
			})
		}
		writeJSON(w, http.StatusOK, item)
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
		// @constraint: instance deletion is only sanctioned after the
		// instance has reached terminal state (terminated_at IS NOT
		// NULL). The terminator worker fires OnInstanceTerminated as
		// soon as the row terminates; a DELETE on an active instance
		// would bypass that trigger and risks firing the lifecycle event
		// against a still-running instance.
		if inst.TerminatedAt == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "instance is not in terminal state; wait for terminated_at to be set",
			})
			return
		}
		// @concept: dry-run — validation passed; skip the fan-out + row
		// delete.
		if WriteDryRunResponse(w, req, "would_have_terminated", map[string]any{
			"instance_id": inst.ID.String(),
		}) {
			return
		}
		// @deliberate: fire OnInstanceTerminated to every store
		// referenced by the instance's template before deleting the
		// row. FanOutInstanceEvent deletes per-store lifecycle rows on
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
			// @constraint: close the instance's main run-scope and fire
			// OnRunScopeTerminal before OnInstanceTerminated, so the
			// host-agent-proxy can reap any spawned processes scoped to
			// this main run-scope. Synchronous in the request context;
			// tpl is the template already loaded above — reuse it.
			if inst.MainRunScopeID != (foundationshared.UUID{}) {
				if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
					return deps.Persist.RunScopes().Close(ctx, tx, inst.MainRunScopeID)
				}); err != nil {
					if deps.Logger != nil {
						deps.Logger.Warn("handleDeleteInstance: close main run-scope failed",
							"instance_id", inst.ID.String(),
							"main_run_scope_id", inst.MainRunScopeID.String(),
							"error", err.Error())
					}
				} else {
					_, _, _ = FanOutRunScopeEvent(req.Context(), deps, tpl.Spec,
						inst.MainRunScopeID, inst.ID, "instance_deleted", nil)
				}
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
			// @deliberate: template gone (e.g. force-deleted); fall back to
			// fanning out via the recorded lifecycle rows so any per-instance
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
		// @constraint: walk this instance's mounting + active
		// publisher-subscriptions, call `Publisher.Unsubscribe` on each,
		// and flip the rows to stopped (mounting rows are force-stopped
		// even when Unsubscribe fails, so the reconciler never re-drives
		// a terminated instance). Non-blocking — failures are logged.
		// The subscription rows are cascade-deleted with the instance
		// row below, so a surviving row is not the retry mechanism: a
		// publisher-side leftover whose Unsubscribe failed here is
		// reaped by the startup resync's orphan sweep, which
		// unsubscribes publisher-reported subscriptions with no backing
		// row.
		if err := runtime.StopPublisherSubscriptionsForInstance(req.Context(), runtime.PublisherLifecycleDeps{
			Persist:    deps.Persist,
			Publishers: deps.Publishers,
			Clock:      deps.Clock,
			Logger:     deps.Logger,
		}, inst.ID); err != nil && deps.Logger != nil {
			deps.Logger.Warn("handleDeleteInstance: stop publisher subscriptions failed",
				"instance_id", inst.ID.String(),
				"error", err.Error())
		}
		// @constraint: walk held-durable claim_handles for this instance
		// and call `ClaimProducer.Release` on each before dropping rows.
		// Per `@blessed-invariant 22` durable claim handles persist
		// past auto-terminal; the only sanctioned release paths are the
		// operator-driven asset delete and instance-termination cleanup.
		// Without this, an instance with held-durable assets would leave
		// producer-side state dangling forever. Failures are logged +
		// retained on the row for retry rather than blocking instance
		// deletion.
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
		// @constraint: any remaining lifecycle rows for scope='instance'
		// on this id are deleted with the instance.
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

// handleTerminateInstance force-terminates an instance: it marks the
// instance terminal (sets terminated_at) and force-fails every
// resource-holding in-flight node-run, abandoning each run's
// uncommitted claim handles. Per spec
// Feature 2 — this is the first production instance-teardown path
// (MarkTerminated was previously test-only), and the only path that can
// rescue a node wedged in `running` awaiting an async callback that
// never arrives.
//
// Relationship to DELETE: terminate makes the instance *terminal* but
// does NOT remove the row or free the instance_key — DELETE remains the
// reaper, and its 409 terminal guard now passes once terminate has run.
// Held-DURABLE claim release (runtime.ReleaseHeldDurableClaims) stays
// DELETE's job; terminate only abandons the uncommitted in-flight claims
// of the node-runs it force-fails.
//
// Force-fail scope: only node-runs surfaced as `running` (incl. the
// await_async-stuck case) or `parked` are torn down — those hold/await a
// claim and carry a non-nil RunScopeID. `fresh`/`stale` node-runs hold
// no claim, are not dispatched, and have a nil RunScopeID, so a
// terminated instance's pending nodes are left inert.
func handleTerminateInstance(deps AppDeps) http.HandlerFunc {
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
		redact := instanceRedact(req.Context(), deps, inst.TemplateHash, inst.ID)
		// @constraint: idempotent — an already-terminal instance returns
		// its current projection with 200 and mutates nothing.
		if inst.TerminatedAt != nil {
			writeJSON(w, http.StatusOK, toInstanceItem(*inst, redact))
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			// @constraint: `json.Decoder.Decode` returns io.EOF on empty
			// input — reason is optional, so only true JSON errors are
			// surfaced.
			if !errors.Is(err, io.EOF) {
				badRequest(w, "invalid JSON body: "+err.Error())
				return
			}
		}
		reason := body.Reason

		// @deliberate: the resource-holding (running | parked) node-runs are
		// computed once and reused for both the dry-run projection and the real
		// teardown so the projection cannot disagree with what teardown does.
		var toFail []persistence.NodeRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			nodes, err := deps.Persist.Nodes().ListByInstance(ctx, inst.ID, tx)
			if err != nil {
				return err
			}
			toFail = resourceHoldingNodeRuns(nodes)
			return nil
		}); err != nil {
			writeError(w, err)
			return
		}

		// @concept: dry-run — validation passed; list what would be
		// force-failed and mutate nothing.
		wouldFail := make([]string, 0, len(toFail))
		for _, n := range toFail {
			wouldFail = append(wouldFail, n.ID.String())
		}
		if WriteDryRunResponse(w, req, "would_have_terminated", map[string]any{
			"instance_id":          inst.ID.String(),
			"reason":               reason,
			"would_fail_node_runs": wouldFail,
		}) {
			return
		}

		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			// @constraint: (a) force-fail the resource-holding node-runs.
			// Re-list inside the teardown tx so the force-fail acts on
			// current state, not the snapshot taken above for the
			// dry-run preview — that closes the window where a node-run
			// could leave running/parked between the preview gather and
			// here (which would make its → failed transition illegal and
			// roll the whole teardown back). Each row carries a non-nil
			// RunScopeID (projected from its in-flight run); a terminal
			// → failed transition passes a non-nil settling signal.
			nodes, err := deps.Persist.Nodes().ListByInstance(ctx, inst.ID, tx)
			if err != nil {
				return err
			}
			for _, run := range resourceHoldingNodeRuns(nodes) {
				if run.RunScopeID == nil {
					// @deliberate: ListByInstance projects RunScopeID for any
					// running/parked row, so this branch is unreachable in
					// practice; the guard exists so a future regression cannot
					// nil-deref.
					continue
				}
				sig := "terminal/error/instance_killed"
				if err := deps.Persist.Nodes().UpdateState(ctx, run.ID, *run.RunScopeID,
					cascade.NodeStateFailed, cascade.ReasonInstanceKilled, &sig, tx); err != nil {
					return err
				}
				abandonInFlightClaims(ctx, deps, run.ID, tx)
			}
			// @constraint: (b) close the instance's main run-scope
			// (idempotent — the UPDATE no-ops if it is already closed).
			// Done in the teardown tx so a terminated instance never
			// carries an open main run-scope. Terminate closes it itself
			// rather than leaving it to the instance_terminator worker —
			// that worker's sweep only covers terminated instances that
			// still carry lifecycle-subscriber bookkeeping rows, so an
			// instance with no lifecycle subscribers would otherwise
			// keep its run-scope open until DELETE.
			if inst.MainRunScopeID != (foundationshared.UUID{}) {
				if err := deps.Persist.RunScopes().Close(ctx, tx, inst.MainRunScopeID); err != nil {
					return err
				}
			}
			// @constraint: (c) mark the instance terminal (idempotent
			// UPDATE).
			if err := deps.Persist.Instances().MarkTerminated(ctx, inst.ID, tx); err != nil {
				return err
			}
			// @constraint: (d) record the teardown cause as an
			// administrative audit row. Kind `instance_terminated` is
			// the underscore administrative form (matching
			// work_started / message_emitted); the slash form is
			// reserved for the concept:signal type-path taxonomy.
			return deps.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &inst.ID,
				Kind:       events.KindInstanceTerminated(),
				Payload:    map[string]any{"reason": reason},
			}, tx)
		}); err != nil {
			writeError(w, err)
			return
		}

		// @constraint: stop this instance's publisher subscriptions now
		// that it is terminal — Unsubscribe each mounting/active
		// subscription and flip the rows to stopped (same call DELETE
		// makes). Without this, the reconciler would keep driving the
		// terminated instance's mounting rows to active, creating
		// publisher-side subscriptions whose every emit is rejected with
		// errInstanceTerminated. Non-blocking — failures are logged; the
		// reconciler's terminated-instance check and DELETE's repeat
		// call are the backstops.
		if err := runtime.StopPublisherSubscriptionsForInstance(req.Context(), runtime.PublisherLifecycleDeps{
			Persist:    deps.Persist,
			Publishers: deps.Publishers,
			Clock:      deps.Clock,
			Logger:     deps.Logger,
		}, inst.ID); err != nil && deps.Logger != nil {
			deps.Logger.Warn("handleTerminateInstance: stop publisher subscriptions failed",
				"instance_id", inst.ID.String(),
				"error", err.Error())
		}

		// @constraint: fire OnRunScopeTerminal for the now-closed main
		// run-scope so the host-agent-proxy reaps any processes spawned
		// under it — the same fan-out handleDeleteInstance and the
		// terminator worker perform. After-commit because it does
		// subscriber / host-agent RPCs; at-least-once (the worker
		// re-fires for subscriber-backed instances and store handlers
		// are idempotent). A template-load failure only skips the
		// fan-out — the run-scope is already closed in the committed tx
		// above.
		if inst.MainRunScopeID != (foundationshared.UUID{}) {
			var tpl *persistence.TemplateRow
			if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
				t, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
				tpl = t
				return err
			}); err != nil {
				if deps.Logger != nil {
					deps.Logger.Warn("handleTerminateInstance: load template for run-scope fan-out failed",
						"instance_id", inst.ID.String(), "error", err.Error())
				}
			} else if tpl != nil {
				_, _, _ = FanOutRunScopeEvent(req.Context(), deps, tpl.Spec,
					inst.MainRunScopeID, inst.ID, "instance_terminated", nil)
			}
		}

		updated, err := resolveInstance(req.Context(), deps, inst.ID.String())
		if err != nil {
			writeError(w, err)
			return
		}
		if updated == nil {
			notFoundResp(w, foundationshared.ErrInstanceNotFound.Error())
			return
		}
		writeJSON(w, http.StatusOK, toInstanceItem(*updated, redact))
	}
}

// resourceHoldingNodeRuns filters a node-row listing down to the
// resource-holding non-terminal runs (running | parked) — the rows the
// force-terminate handler tears down. `fresh`/`stale` rows hold no claim
// and are not dispatched, so they are excluded; their RunScopeID is nil.
func resourceHoldingNodeRuns(nodes []persistence.NodeRow) []persistence.NodeRow {
	var out []persistence.NodeRow
	for _, n := range nodes {
		if n.State == cascade.NodeStateRunning || n.State == cascade.NodeStateParked {
			out = append(out, n)
		}
	}
	return out
}

// abandonInFlightClaims abandons a node-run's in-flight (active,
// uncommitted) claim handles during force-terminate. Mirrors the
// best-effort discipline in handleDeleteInstance's claim release:
// abandon failures are WARN-logged and non-fatal so a producer-side
// hiccup can't wedge the operator's teardown — the run row is already
// transitioned to failed in the same tx.
//
// Committed-durable claims are NOT touched here: their release stays
// DELETE's job (runtime.ReleaseHeldDurableClaims). Only `active`-state
// rows are promoted to `abandoned`.
func abandonInFlightClaims(ctx context.Context, deps AppDeps, nodeID foundationshared.UUID, tx persistence.Tx) {
	handles, err := deps.Persist.ClaimHandles().ListByHolderNode(ctx, nodeID, tx)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Warn("handleTerminateInstance: list claim handles for force-fail failed",
				"node_id", nodeID.String(), "error", err.Error())
		}
		return
	}
	for _, h := range handles {
		if h.State != spec.ClaimHandleStateActive || h.HolderSupervisorID == nil {
			continue
		}
		if err := deps.Persist.ClaimHandles().Promote(ctx, h.ID, *h.HolderSupervisorID,
			spec.ClaimHandleStateAbandoned, tx); err != nil && deps.Logger != nil {
			deps.Logger.Warn("handleTerminateInstance: abandon in-flight claim failed",
				"node_id", nodeID.String(), "claim_id", h.ID.String(), "error", err.Error())
		}
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
	InstanceKey        *string
	Params             map[string]any
	AttributeOverrides map[string]any
	// AttributeOverridesMatchCounts is the initial per-entry counter
	// array for AttributeOverrides.by_match. Length equals
	// len(by_match); nil for instances with no by_match entries.
	// Persisted verbatim on rimsky_instances.attribute_overrides_match_counts.
	AttributeOverridesMatchCounts []int64
	// FrameDeliveryMode is one of "serial_queue" / "coalesce". Empty
	// string falls through to the column DEFAULT 'coalesce'.
	FrameDeliveryMode string
	// Paused is the create-time hold flag. Threaded through to the
	// persistence layer's InstanceCreateInput.Paused. Per concept:breakpoint.
	Paused bool
	// TerminateAfterRun is the create-time opt-in self-termination flag.
	// Threaded through to InstanceCreateInput.TerminateAfterRun. Per
	// concept:instance.
	TerminateAfterRun bool
	// ServiceBindings is the per-instance late-bound service catalog
	// (opaque JSON), threaded verbatim onto InstanceCreateInput.ServiceBindings.
	ServiceBindings json.RawMessage
	// CreatedByAPIKeyID is the api-key that authenticated the create
	// request, threaded onto InstanceCreateInput.CreatedByAPIKeyID. Nil
	// for instances created under anonymous-mode.
	CreatedByAPIKeyID *foundationshared.UUID
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
	// @concept: run-scope — allocate the instance + main RunScope ids up
	// front so the two inserts (rimsky_run_scopes, rimsky_instances)
	// reference each other. Every instance has exactly one main RunScope
	// rooted at the top of the run-tree (parent_run_scope_id IS NULL,
	// parent_run_id IS NULL, graph_name = spec.MainGraphName).
	// rimsky_instances.main_run_scope_id has an FK to
	// rimsky_run_scopes(id), so the RunScope insert must precede the
	// instance insert. The RunScope row in turn has
	// rimsky_run_scopes.instance_id → rimsky_instances(id), which is
	// satisfied because both FKs are declared `DEFERRABLE INITIALLY
	// DEFERRED` — the FK check is postponed to COMMIT, by which time
	// both rows exist.
	instanceID := foundationshared.UUID(uuid.New())
	mainRunScopeID := foundationshared.UUID(uuid.New())
	if err := deps.Persist.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
		ID:               mainRunScopeID,
		ParentRunScopeID: nil,
		ParentRunID:      nil,
		GraphName:        spec.MainGraphName,
		PartitionKey:     "",
		InstanceID:       instanceID,
	}); err != nil {
		return createInstanceResponse{}, fmt.Errorf("instance-factory: create main run scope: %w", err)
	}
	// @constraint: create instance row (fails with
	// ErrInstanceKeyConflict if duplicate).
	inst, err := deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:                            instanceID,
		TemplateHash:                  tpl.ID,
		InstanceKey:                   args.InstanceKey,
		Params:                        args.Params,
		AttributeOverrides:            args.AttributeOverrides,
		AttributeOverridesMatchCounts: args.AttributeOverridesMatchCounts,
		FrameDeliveryMode:             args.FrameDeliveryMode,
		MainRunScopeID:                mainRunScopeID,
		Paused:                        args.Paused,
		TerminateAfterRun:             args.TerminateAfterRun,
		ServiceBindings:               args.ServiceBindings,
		CreatedByAPIKeyID:             args.CreatedByAPIKeyID,
	}, tx)
	if err != nil {
		return createInstanceResponse{}, err
	}

	// @constraint: allocate one UUID per node up-front so dependencies[]
	// can be rewritten.
	nodeIDs := make(map[string]foundationshared.UUID, len(tpl.Spec.Nodes))
	for _, def := range tpl.Spec.Nodes {
		nodeIDs[def.Type] = uuid.New()
	}

	// @constraint: params marshalled once for the materialization-time tag
	// substitution pass (Item 4) with a params-only ResolveContext — other
	// substitution kinds aren't available at instance-creation time. Failures
	// here surface as 400-class errors to the caller. See `resolveNodeTags`.
	var paramsBytes json.RawMessage
	if args.Params != nil {
		b, merr := json.Marshal(args.Params)
		if merr != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: marshal params for tag substitution: %w", merr)
		}
		paramsBytes = b
	}

	// @constraint: phase 1 creates nodes (Create defaults to 'fresh' per
	// the baseline schema) + registers schedules. Phase 2 enqueues an
	// initial frame for each root. Cascade-coupling is declared
	// receiver-side via `subscribes:`; the per-template
	// subscription-edge inverse map drives cascade walks.
	for _, def := range tpl.Spec.Nodes {
		nodeID := nodeIDs[def.Type]
		// @constraint: subscription validity is checked at
		// template-deploy time by the validator; emit an instance-time
		// error on missing target too, so a hand-rolled spec doesn't
		// silently dispatch.
		for _, s := range def.Subscribes {
			if s.Node == "" {
				continue
			}
			if _, ok := nodeIDs[s.Node]; !ok {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: subscribe references unknown node %q (on node %q)", s.Node, def.Type)
			}
		}

		// @constraint: resolve operator-facing tags against instance
		// params. Failures here are fatal at instance creation, matching
		// the dispatch-time discipline for required-attribute
		// substitution.
		resolvedTags, terr := resolveNodeTags(def.Tags, paramsBytes)
		if terr != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: resolve tags on node %q: %w", def.Type, terr)
		}

		// @constraint: create node row. The per-node `schedule:` field
		// and the rimsky_schedules table are retired; the bundled
		// `sensor-cron` service owns cron firing via the Sensor
		// protocol.
		if _, err := deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: inst.ID,
			NodeType:   def.Type,
			Executor:   def.Executor,
			Tags:       resolvedTags,
		}, tx); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create node %q: %w", def.Type, err)
		}
	}

	// @constraint: phase 2 enqueues an initial frame for each root node
	// (no upstream subscriptions), reusing the caller's tx so the frame
	// inserts are atomic with the instance+node creation above. A "root"
	// is a node with no `subscribes:` entries naming an upstream node
	// AND no substitution refs in its attribute schema. Cross-cutting
	// (`instance:true`) entries don't disqualify a root because they
	// fire on cascade-walks, not at instance create.
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
