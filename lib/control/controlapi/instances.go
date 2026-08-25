// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: host-daemon-proxy
// @concept: anonymous-mode
func resolveTargetRoutingIdentity(ident auth.Identity, requestedTargetDaemon string) (string, error) {
	if ident.KeyID != nil {
		return ident.KeyID.String(), nil
	}
	if requestedTargetDaemon == "" {
		return "", fmt.Errorf("target_daemon required for anonymous-mode instance creation")
	}
	if err := sillyname.Validate(requestedTargetDaemon); err != nil {
		return "", fmt.Errorf("target_daemon: %w", err)
	}
	return requestedTargetDaemon, nil
}

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
	Template           string          `json:"template"`
	InstanceKey        *string         `json:"instance_key,omitempty"`
	Params             map[string]any  `json:"params,omitempty"`
	AttributeOverrides map[string]any  `json:"attribute_overrides,omitempty"`
	Paused             bool            `json:"paused,omitempty"`
	ServiceBindings    json.RawMessage `json:"service_bindings,omitempty"`
	// @concept: instance
	MessageQueueMode string `json:"message_queue_mode,omitempty"`
	// @concept: host-daemon-proxy
	// @concept: anonymous-mode
	TargetDaemon string `json:"target_daemon,omitempty"`
}

type createInstanceResponse struct {
	InstanceID   string  `json:"instance_id"`
	TemplateHash string  `json:"template_hash"`
	InstanceKey  *string `json:"instance_key,omitempty"`
	NodeCount    int     `json:"node_count"`
}

type instanceItem struct {
	ID                            string                     `json:"id"`
	TemplateHash                  string                     `json:"template_hash"`
	InstanceKey                   *string                    `json:"instance_key,omitempty"`
	Params                        map[string]any             `json:"params"`
	AttributeOverrides            map[string]any             `json:"attribute_overrides,omitempty"`
	AttributeOverridesMatchCounts []int64                    `json:"attribute_overrides_match_counts,omitempty"`
	Paused                        bool                       `json:"paused"`
	CreatedAt                     time.Time                  `json:"created_at"`
	TerminatedAt                  *time.Time                 `json:"terminated_at,omitempty"`
	ServiceBindings               json.RawMessage            `json:"service_bindings,omitempty"`
	CreatedByAPIKeyID             string                     `json:"created_by_api_key_id,omitempty"`
	TargetRoutingIdentity         string                     `json:"target_routing_identity,omitempty"`
	Subscriptions                 []instanceSubscriptionItem `json:"subscriptions,omitempty"`
	// @concept: instance
	MessageQueueMode string `json:"message_queue_mode"`
}

type instanceSubscriptionItem struct {
	ID            string    `json:"id"`
	PublisherName string    `json:"publisher_name"`
	Kind          string    `json:"kind"`
	MessageType   string    `json:"message_type"`
	State         string    `json:"state"`
	StartedAt     time.Time `json:"started_at"`
	FailureReason string    `json:"failure_reason,omitempty"`
}

func toInstanceItem(r persistence.InstanceRow, matchCounts []int64) instanceItem {
	out := instanceItem{
		ID:                    r.ID.String(),
		TemplateHash:          r.TemplateHash,
		InstanceKey:           r.InstanceKey,
		Params:                r.Params,
		Paused:                r.Paused,
		CreatedAt:             r.CreatedAt,
		TerminatedAt:          r.TerminatedAt,
		TargetRoutingIdentity: r.TargetRoutingIdentity,
		MessageQueueMode:      r.MessageQueueMode,
	}
	if len(r.AttributeOverrides) > 0 {
		out.AttributeOverrides = r.AttributeOverrides
	}
	if len(matchCounts) > 0 {
		out.AttributeOverridesMatchCounts = matchCounts
	}
	if len(r.ServiceBindings) > 0 {
		out.ServiceBindings = r.ServiceBindings
	}
	if r.CreatedByAPIKeyID != nil {
		out.CreatedByAPIKeyID = r.CreatedByAPIKeyID.String()
	}
	return out
}

func overrideMatchCountsFor(ctx context.Context, deps AppDeps, r persistence.InstanceRow) []int64 {
	byMatch, _ := r.AttributeOverrides["by_match"].([]any)
	if len(byMatch) == 0 {
		return nil
	}
	var countsByIdx map[int64]int64
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		countsByIdx, err = deps.Persist.Events().CountAttributeOverrideMatchesByIndex(ctx, r.ID, tx)
		return err
	}); err != nil {
		deps.Logger.Warn("INSTANCE.ATTRIBUTEOVERRIDEMATCHEVENTS.QUERYFAILED",
			"instance_id", r.ID.String(), "error", err.Error())
		return make([]int64, len(byMatch))
	}
	out := make([]int64, len(byMatch))
	for idx, cnt := range countsByIdx {
		if idx < 0 || int(idx) >= len(out) {
			continue
		}
		out[idx] = cnt
	}
	return out
}

func registerInstancesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances", gate(deps, "instance:create", handleCreateInstance(deps)))
	r.Get("/instances", gate(deps, "instance:read", handleListInstances(deps)))
	r.Get("/instances/{idOrKey}", gate(deps, "instance:read", handleGetInstance(deps)))
	r.Delete("/instances/{idOrKey}", gate(deps, "instance:terminate", handleDeleteInstance(deps)))
	r.Post("/instances/{idOrKey}/pause", gate(deps, "instance:pause", handlePauseInstance(deps)))
	r.Post("/instances/{idOrKey}/resume", gate(deps, "instance:resume", handleResumeInstance(deps)))
	r.Post("/instances/{idOrKey}/terminate", gate(deps, "instance:kill", handleTerminateInstance(deps)))
}

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
		if inst.Paused {
			writeError(w, foundationshared.ErrInstanceAlreadyPaused)
			return
		}
		// @concept: dry-run
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
		if !inst.Paused {
			writeError(w, foundationshared.ErrInstanceNotPaused)
			return
		}
		// @concept: dry-run
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
		ident, _ := IdentityFromContextOK(req.Context())
		if body.InstanceKey != nil && *body.InstanceKey == "" {
			body.InstanceKey = nil
		}
		if strings.TrimSpace(body.Template) == "" {
			badRequest(w, "template is required (tag or hash)")
			return
		}
		switch body.MessageQueueMode {
		case "", nodepkg.MessageQueueModeBacklog, nodepkg.MessageQueueModeCoalesce:
		default:
			badRequest(w, fmt.Sprintf("message_queue_mode = %q; want one of backlog | coalesce", body.MessageQueueMode))
			return
		}
		targetRoutingIdentity, terr := resolveTargetRoutingIdentity(ident, body.TargetDaemon)
		if terr != nil {
			badRequest(w, terr.Error())
			return
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

		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
		var (
			respOut                     createInstanceResponse
			existedKey                  bool
			existingOverrides           map[string]any
			existingParams              map[string]any
			fanOutBindings              json.RawMessage
			fanOutOwner                 *foundationshared.UUID
			fanOutTargetRoutingIdentity string
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
			if vErr := validateAttributeOverrides(body.AttributeOverrides, row.Spec.Nodes, row.Spec.Graphs, deps.Executors); vErr != nil {
				return vErr
			}
			if sErr := validateStaticConfigAgainstExecutorSchemas(row.Spec.Nodes, row.Spec.Defaults, deps.ExecutorCapabilities); sErr != nil {
				return sErr
			}
			if isDryRun {
				return errDryRunOK
			}
			if body.InstanceKey != nil {
				existing, err := deps.Persist.Instances().GetByInstanceKey(ctx, hash, *body.InstanceKey, tx)
				if err != nil {
					return err
				}
				if existing != nil {
					existedKey = true
					existingOverrides = existing.AttributeOverrides
					existingParams = existing.Params
					fanOutBindings = existing.ServiceBindings
					fanOutOwner = existing.CreatedByAPIKeyID
					fanOutTargetRoutingIdentity = existing.TargetRoutingIdentity
					respOut = createInstanceResponse{
						InstanceID:   existing.ID.String(),
						TemplateHash: existing.TemplateHash,
						InstanceKey:  existing.InstanceKey,
						NodeCount:    len(row.Spec.Nodes),
					}
					return stageInstanceCreated(ctx, deps, hash, row.Spec, respOut, existingParams,
						fanOutBindings, fanOutOwner, fanOutTargetRoutingIdentity, tx)
				}
			}
			provisioned, err := provisionInstanceTx(ctx, deps, row, provisionArgs{
				InstanceKey:           body.InstanceKey,
				Params:                params,
				AttributeOverrides:    body.AttributeOverrides,
				Paused:                body.Paused,
				ServiceBindings:       body.ServiceBindings,
				CreatedByAPIKeyID:     ident.KeyID,
				TargetRoutingIdentity: targetRoutingIdentity,
				MessageQueueMode:      body.MessageQueueMode,
			}, tx)
			if err != nil {
				return err
			}
			if len(row.Spec.Publishers) > 0 {
				instUUID, parseErr := uuid.Parse(provisioned.InstanceID)
				if parseErr != nil {
					deps.Logger.Error("INSTANCE.PUBLISHERSUBSCRIPTIONS.INSTANCEIDPARSEFAILED",
						"instance_id", provisioned.InstanceID,
						"error", parseErr.Error())
					return fmt.Errorf("parse provisioned instance id %q: %w", provisioned.InstanceID, parseErr)
				}
				if subErr := runtime.StartPublisherSubscriptionsForInstance(ctx, runtime.PublisherLifecycleDeps{
					Persist:    deps.Persist,
					Publishers: deps.Publishers,
					Clock:      deps.Clock,
					Logger:     deps.Logger,
				}, foundationshared.UUID(instUUID), params, row.Spec.Publishers, tx); subErr != nil {
					deps.Logger.Error("INSTANCE.PUBLISHERSUBSCRIPTIONS.INSERTFAILED",
						"instance_id", provisioned.InstanceID,
						"template_hash", hash,
						"error", subErr.Error())
					return fmt.Errorf("insert publisher-subscription rows for instance %s: %w", provisioned.InstanceID, subErr)
				}
			}
			fanOutBindings = body.ServiceBindings
			fanOutOwner = ident.KeyID
			fanOutTargetRoutingIdentity = targetRoutingIdentity
			respOut = provisioned
			return stageInstanceCreated(ctx, deps, hash, row.Spec, respOut, params,
				fanOutBindings, fanOutOwner, fanOutTargetRoutingIdentity, tx)
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
		deliverStagedLifecycleAfterCommit(req.Context(), deps,
			persistence.LifecycleScopeInstance, respOut.InstanceID,
			"instance_id", respOut.InstanceID, "template_hash", hash,
			"event", lifecycle.EventInstanceCreated.String())
		status := http.StatusCreated
		if existedKey {
			status = http.StatusOK
		}
		if !existedKey && len(body.AttributeOverrides) > 0 {
			byExecutor, byNode, byMatchCount := overridePresentKeys(body.AttributeOverrides)
			deps.Logger.Info("INSTANCE.ATTRIBUTEOVERRIDES.ATTACHED",
				"instance_id", respOut.InstanceID,
				"template_hash", respOut.TemplateHash,
				"by_executor", byExecutor,
				"by_node", byNode,
				"by_match_count", byMatchCount)
		}
		if existedKey && len(body.AttributeOverrides) > 0 && !overridesEqual(body.AttributeOverrides, existingOverrides) {
			byExecutor, byNode, byMatchCount := overridePresentKeys(body.AttributeOverrides)
			deps.Logger.Warn("INSTANCE.ATTRIBUTEOVERRIDES.REPLACED", "detail", "an idempotent match replaced them",
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
			switch v {
			case "1", "true":
				b := true
				filter.Active = &b
			case "0", "false":
				b := false
				filter.Active = &b
			default:
				badRequest(w, fmt.Sprintf("active = %q; want one of 1 | true | 0 | false", v))
				return
			}
		}
		limit, err := parseLimit(req, 100)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		pag := persistence.ListPagination{
			Limit:  limit,
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
		items := make([]instanceItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, toInstanceItem(r, overrideMatchCountsFor(req.Context(), deps, r)))
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
		item := toInstanceItem(*inst, overrideMatchCountsFor(req.Context(), deps, *inst))
		// @concept: publisher-subscription
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
				MessageType:   s.MessageType,
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
		if inst.TerminatedAt == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "instance is not in terminal state; wait for terminated_at to be set",
			})
			return
		}
		// @concept: dry-run
		if WriteDryRunResponse(w, req, "would_have_deleted_instance", map[string]any{
			"instance_id": inst.ID.String(),
		}) {
			return
		}
		if err := runtime.StopPublisherSubscriptionsForInstance(req.Context(), runtime.PublisherLifecycleDeps{
			Persist:    deps.Persist,
			Publishers: deps.Publishers,
			Clock:      deps.Clock,
			Logger:     deps.Logger,
		}, inst.ID); err != nil && deps.Logger != nil {
			deps.Logger.Warn("INSTANCE.PUBLISHERSUBSCRIPTIONS.STOPFAILED", "site", "handleDeleteInstance",
				"instance_id", inst.ID.String(),
				"error", err.Error())
		}
		if deps.ClaimProducers != nil {
			if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
				_, rErr := runtime.ReleaseCommittedDurableClaims(ctx, runtime.RunArgs{
					Persist:               deps.Persist,
					ClaimHandles:          deps.Persist.ClaimHandles(),
					ClaimProducerRegistry: deps.ClaimProducers,
					Clock:                 deps.Clock,
					Logger:                deps.Logger,
				}, inst.ID, deps.Logger, tx)
				return rErr
			}); err != nil && deps.Logger != nil {
				deps.Logger.Warn("INSTANCE.DURABLECLAIMS.RELEASEFAILED", "site", "handleDeleteInstance",
					"instance_id", inst.ID.String(),
					"error", err.Error())
			}
		}
		var closedScopes []foundationshared.UUID
		// @decision: lifecycle-fanout-after-commit
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			scopes, err := closeAndStageRunScopeTerminalsInTx(ctx, deps, inst.ID, instanceTerminatedReason, tx)
			if err != nil {
				return err
			}
			closedScopes = scopes
			return deps.Persist.Instances().Delete(ctx, inst.ID, tx)
		}); err != nil {
			writeError(w, err)
			return
		}
		deliverInstanceTerminationAfterCommit(req.Context(), deps, inst.ID, closedScopes)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

const instanceTerminatedReason = "instance_terminated"

// @decision: lifecycle-fanout-after-commit
// @decision: lifecycle-drain-per-role
func deliverInstanceTerminationAfterCommit(
	ctx context.Context, deps AppDeps, instanceID foundationshared.UUID, closedScopes []foundationshared.UUID,
) {
	for _, scopeID := range closedScopes {
		deliverStagedLifecycleAfterCommit(ctx, deps,
			persistence.LifecycleScopeRunScope, scopeID.String(),
			"instance_id", instanceID.String())
	}
	deliverStagedLifecycleAfterCommit(ctx, deps,
		persistence.LifecycleScopeInstance, instanceID.String(),
		"instance_id", instanceID.String())
}

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
		if inst.TerminatedAt != nil {
			// @concept: dry-run
			if WriteDryRunResponse(w, req, "would_have_terminated", map[string]any{
				"instance_id":        inst.ID.String(),
				"already_terminated": true,
			}) {
				return
			}
			writeJSON(w, http.StatusOK, toInstanceItem(*inst, overrideMatchCountsFor(req.Context(), deps, *inst)))
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			if !errors.Is(err, io.EOF) {
				badRequest(w, "invalid JSON body: "+err.Error())
				return
			}
		}
		reason := body.Reason

		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
		if isDryRun {
			var toFail []persistence.NodeRunLatest
			if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
				runs, err := deps.Persist.Nodes().ListRunsForInstanceByStates(ctx, inst.ID, cascade.InFlightStates, tx)
				if err != nil {
					return err
				}
				toFail = runs
				return nil
			}); err != nil {
				writeError(w, err)
				return
			}
			wouldFail := make([]string, 0, len(toFail))
			for _, r := range toFail {
				wouldFail = append(wouldFail, r.NodeID.String())
			}
			// @concept: dry-run
			WriteDryRunResponseForced(w, "would_have_terminated", map[string]any{
				"instance_id":          inst.ID.String(),
				"reason":               reason,
				"would_fail_node_runs": wouldFail,
			})
			return
		}

		if deps.AdvisoryLocker == nil {
			writeError(w, runtime.ErrLifecycleAdvisoryLockerMissing)
			return
		}
		var alreadyTerminated bool
		var killPostCommit func(context.Context)
		var closedScopes []foundationshared.UUID
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if err := deps.AdvisoryLocker.TakeLifecycleScopeLock(ctx,
				persistence.LifecycleScopeInstance, inst.ID.String(), tx); err != nil {
				return err
			}
			fresh, err := deps.Persist.Instances().Get(ctx, inst.ID, tx)
			if err != nil {
				return err
			}
			if fresh == nil {
				return foundationshared.ErrInstanceNotFound
			}
			if fresh.TerminatedAt != nil {
				alreadyTerminated = true
				return nil
			}
			post, err := runtime.ForceFailInFlightRunsForInstance(ctx, terminateRunArgs(deps), inst.ID, tx)
			if err != nil {
				return err
			}
			killPostCommit = post
			// @concept: message
			if _, err := deps.Persist.Messages().CancelPendingForInstance(ctx, inst.ID, tx); err != nil {
				return err
			}
			// @concept: frame
			if _, err := deps.Persist.Frames().MarkOpenFramesEndedForInstance(ctx, inst.ID, tx); err != nil {
				return err
			}
			if err := deps.Persist.Instances().MarkTerminated(ctx, inst.ID, tx); err != nil {
				return err
			}
			if err := deps.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &inst.ID,
				Kind:       events.KindInstanceTerminated(),
				Payload:    eventpayload.New(&genv1.InstanceTerminatedPayload{Reason: reason}),
			}, tx); err != nil {
				return err
			}
			// @decision: lifecycle-fanout-after-commit
			scopes, err := closeAndStageRunScopeTerminalsInTx(ctx, deps, inst.ID, instanceTerminatedReason, tx)
			if err != nil {
				return err
			}
			closedScopes = scopes
			terminated, err := deps.Persist.Instances().Get(ctx, inst.ID, tx)
			if err != nil {
				return err
			}
			if terminated == nil {
				return foundationshared.ErrInstanceNotFound
			}
			var terminatedAtMs int64
			if terminated.TerminatedAt != nil {
				terminatedAtMs = terminated.TerminatedAt.UnixMilli()
			}
			return stageInstanceTerminatedInTx(ctx, deps, *terminated, terminatedAtMs, tx)
		}); err != nil {
			writeError(w, err)
			return
		}

		if !alreadyTerminated {
			if killPostCommit != nil {
				killPostCommit(req.Context())
			}

			if err := runtime.StopPublisherSubscriptionsForInstance(req.Context(), runtime.PublisherLifecycleDeps{
				Persist:    deps.Persist,
				Publishers: deps.Publishers,
				Clock:      deps.Clock,
				Logger:     deps.Logger,
			}, inst.ID); err != nil && deps.Logger != nil {
				deps.Logger.Warn("INSTANCE.PUBLISHERSUBSCRIPTIONS.STOPFAILED", "site", "handleTerminateInstance",
					"instance_id", inst.ID.String(),
					"error", err.Error())
			}

			deliverInstanceTerminationAfterCommit(req.Context(), deps, inst.ID, closedScopes)
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
		writeJSON(w, http.StatusOK, toInstanceItem(*updated, overrideMatchCountsFor(req.Context(), deps, *updated)))
	}
}

// @concept: terminal-resolution
func terminateRunArgs(deps AppDeps) runtime.RunArgs {
	return runtime.RunArgs{
		Persist:               deps.Persist,
		Queue:                 deps.Queue,
		AdvisoryLocker:        deps.AdvisoryLocker,
		ClaimHandles:          deps.Persist.ClaimHandles(),
		ClaimProducerRegistry: deps.ClaimProducers,
		DataProcessors:        deps.DataProcessors,
		Clock:                 deps.Clock,
		Logger:                deps.Logger,
		SupervisorID:          "control-api-terminate",
	}
}

type provisionArgs struct {
	InstanceKey           *string
	Params                map[string]any
	AttributeOverrides    map[string]any
	Paused                bool
	ServiceBindings       json.RawMessage
	CreatedByAPIKeyID     *foundationshared.UUID
	TargetRoutingIdentity string
	// @concept: instance
	MessageQueueMode string
}

func provisionInstanceTx(
	ctx context.Context, deps AppDeps, tpl *persistence.TemplateRow, args provisionArgs, tx persistence.Tx,
) (createInstanceResponse, error) {
	instanceID := foundationshared.UUID(uuid.New())
	queueMode := tpl.Spec.MessageQueueMode
	if queueMode == "" {
		queueMode = "backlog"
	}
	// @concept: instance
	if args.MessageQueueMode != "" {
		queueMode = args.MessageQueueMode
	}
	inst, err := deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:                    instanceID,
		TemplateHash:          tpl.ID,
		InstanceKey:           args.InstanceKey,
		Params:                args.Params,
		AttributeOverrides:    args.AttributeOverrides,
		Paused:                args.Paused,
		ServiceBindings:       args.ServiceBindings,
		CreatedByAPIKeyID:     args.CreatedByAPIKeyID,
		TargetRoutingIdentity: args.TargetRoutingIdentity,
		MessageQueueMode:      queueMode,
	}, tx)
	if err != nil {
		return createInstanceResponse{}, err
	}

	nodeIDs := make(map[string]foundationshared.UUID, len(tpl.Spec.Nodes))
	for _, def := range tpl.Spec.Nodes {
		nodeIDs[def.Type] = uuid.New()
	}

	// @concept: message-schema
	declaredMessageTypes := make(map[string]struct{}, len(tpl.Spec.Messages))
	for _, m := range tpl.Spec.Messages {
		declaredMessageTypes[m.Type] = struct{}{}
	}

	var paramsBytes json.RawMessage
	if args.Params != nil {
		b, merr := json.Marshal(args.Params)
		if merr != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: marshal params for tag substitution: %w", merr)
		}
		paramsBytes = b
	}

	for _, def := range tpl.Spec.Nodes {
		nodeID := nodeIDs[def.Type]
		for _, s := range def.Subscribes {
			if s.Node == "" {
				continue
			}
			if _, ok := nodeIDs[s.Node]; ok {
				continue
			}
			// @concept: message-schema
			if _, ok := declaredMessageTypes[s.Node]; ok {
				continue
			}
			return createInstanceResponse{}, fmt.Errorf("instance-factory: subscribe references unknown node %q (on node %q)", s.Node, def.Type)
		}

		resolvedTags, terr := resolveNodeTags(def.Tags, paramsBytes)
		if terr != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: resolve tags on node %q: %w", def.Type, terr)
		}

		if _, err := deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:          nodeID,
			InstanceID:  inst.ID,
			NodeType:    def.Type,
			Executor:    def.Executor,
			Tags:        resolvedTags,
			CascadeMode: def.CascadeMode,
		}, tx); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create node %q: %w", def.Type, err)
		}
	}

	// @concept: message
	// @concept: message-schema
	// @decision: empty-message-as-root-trigger
	// @story: empty-message-wakes-roots
	receiverTypes := make([]string, 0, len(tpl.Spec.Messages)+1)
	receiverTypes = append(receiverTypes, "")
	for _, m := range tpl.Spec.Messages {
		receiverTypes = append(receiverTypes, m.Type)
	}
	for _, t := range receiverTypes {
		if _, err := deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         foundationshared.UUID(uuid.New()),
			InstanceID: inst.ID,
			NodeType:   t,
			Executor:   "",
		}, tx); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create message-receiver node %q: %w", t, err)
		}
	}

	// @concept: instance
	// @story: instance-create-is-idle
	// @decision: synthetic-envelope-mechanism-retired

	return createInstanceResponse{
		InstanceID:   inst.ID.String(),
		TemplateHash: inst.TemplateHash,
		InstanceKey:  inst.InstanceKey,
		NodeCount:    len(tpl.Spec.Nodes),
	}, nil
}
