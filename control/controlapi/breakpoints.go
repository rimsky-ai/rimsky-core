// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoints.go — HTTP+JSON surface for the instance-debugger
// breakpoint primitive per spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md §4.
//
//	POST   /instances/{idOrKey}/breakpoints
//	GET    /instances/{idOrKey}/breakpoints
//	DELETE /instances/{idOrKey}/breakpoints/{breakpoint_id}
//	POST   /instances/{idOrKey}/breakpoints/{breakpoint_id}/resume
//
// Handlers stay thin: parse JSON → validate transport-level shape →
// call into persistence / matcher / runtime helpers → translate
// sentinels via writeError. Resume-time validation discipline (spec
// §4.7) lives in runtime/breakpoint_resume.go::ValidateAndPersistResume
// so any future transport (MCP, webhook, SSE) shares one entry point.
//
// @concept: breakpoint

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/runtime"
)

// registerBreakpointsRoutes wires the four breakpoint routes per spec
// §4.1 / §4.7.
func registerBreakpointsRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{idOrKey}/breakpoints",
		gate(deps, "breakpoint:create", handleCreateBreakpoint(deps)))
	r.Get("/instances/{idOrKey}/breakpoints",
		gate(deps, "breakpoint:read", handleListBreakpoints(deps)))
	r.Delete("/instances/{idOrKey}/breakpoints/{breakpoint_id}",
		gate(deps, "breakpoint:delete", handleDeleteBreakpoint(deps)))
	r.Post("/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume",
		gate(deps, "breakpoint:resume", handleResumeBreakpointHit(deps)))
}

// createBreakpointRequest is the POST body shape per spec §4.1.
type createBreakpointRequest struct {
	Matcher        map[string]any `json:"matcher,omitempty"`
	Checkpoint     string         `json:"checkpoint"`
	SignalType     *string        `json:"signal_type,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	OverflowPolicy string         `json:"overflow_policy,omitempty"`
	HitTTLSeconds  *int           `json:"hit_ttl_seconds,omitempty"`
	TTLSeconds     *int           `json:"ttl_seconds,omitempty"`
}

// breakpointItem is the JSON projection of persistence.BreakpointRow
// returned by Create and List.
type breakpointItem struct {
	ID             string         `json:"breakpoint_id"`
	InstanceID     string         `json:"instance_id"`
	Matcher        map[string]any `json:"matcher"`
	Checkpoint     string         `json:"checkpoint"`
	SignalType     *string        `json:"signal_type,omitempty"`
	Mode           string         `json:"mode"`
	OverflowPolicy string         `json:"overflow_policy"`
	HitTTLSeconds  int            `json:"hit_ttl_seconds"`
	TTLSeconds     *int           `json:"ttl_seconds,omitempty"`
	DroppedCount   int64          `json:"dropped_count"`
	CreatedByKey   string         `json:"created_by_key"`
	CreatedAt      time.Time      `json:"created_at"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
}

func toBreakpointItem(r persistence.BreakpointRow) breakpointItem {
	return breakpointItem{
		ID:             r.ID.String(),
		InstanceID:     r.InstanceID.String(),
		Matcher:        r.Matcher,
		Checkpoint:     string(r.Checkpoint),
		SignalType:     r.SignalType,
		Mode:           string(r.Mode),
		OverflowPolicy: string(r.OverflowPolicy),
		HitTTLSeconds:  r.HitTTLSeconds,
		TTLSeconds:     r.TTLSeconds,
		DroppedCount:   r.DroppedCount,
		CreatedByKey:   r.CreatedByKey,
		CreatedAt:      r.CreatedAt,
		ExpiresAt:      r.ExpiresAt,
	}
}

// handleCreateBreakpoint installs a new breakpoint on an instance.
// Validates the matcher against the locked template's name sets,
// resolves mode-conditional overflow_policy defaults, and rejects
// invalid (mode, overflow_policy) combinations per spec §4.8. Returns
// 201 with the breakpoint_id on success.
func handleCreateBreakpoint(deps AppDeps) http.HandlerFunc {
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
		var body createBreakpointRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}

		// Checkpoint is the discriminator for several downstream rules
		// (signal_type validity, default overflow_policy) — validate it
		// first so we can reference it below.
		checkpoint, err := parseCheckpoint(body.Checkpoint)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		// Mode defaults to "pause" per spec §4.1.
		mode := persistence.BreakpointModePause
		if body.Mode != "" {
			m, err := parseBreakpointMode(body.Mode)
			if err != nil {
				badRequest(w, err.Error())
				return
			}
			mode = m
		}

		// Overflow policy: empty → mode-conditional default
		// (notify_only → drop_oldest, pause → block_dispatch) per spec
		// §4.8. Validate the (mode, overflow_policy) pair after the
		// defaulting step.
		overflowPolicy, err := resolveOverflowPolicy(body.OverflowPolicy, mode)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		// signal_type: valid only on after_terminal checkpoints; must
		// satisfy signal.ValidateSubscriptionType (admits trailing-*).
		// Per spec §4.5.
		if body.SignalType != nil {
			if checkpoint == persistence.CheckpointBeforeDispatch {
				badRequest(w, "signal_type is only valid on after_terminal checkpoints")
				return
			}
			if err := signal.ValidateSubscriptionType(signal.TypePath(*body.SignalType)); err != nil {
				badRequest(w, err.Error())
				return
			}
		}

		hitTTL := 300
		if body.HitTTLSeconds != nil {
			if *body.HitTTLSeconds <= 0 {
				badRequest(w, "hit_ttl_seconds must be positive")
				return
			}
			hitTTL = *body.HitTTLSeconds
		}
		if body.TTLSeconds != nil && *body.TTLSeconds <= 0 {
			badRequest(w, "ttl_seconds must be positive")
			return
		}

		// Matcher validation needs the locked template's node + executor
		// + graph name sets. Open a tx, lock the template row FOR UPDATE
		// (so concurrent template-deploy / undeploy can't race), and
		// validate.
		var (
			created persistence.BreakpointRow
			bpID    foundationshared.UUID
		)
		txErr := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			tpl, err := deps.Persist.Templates().LockForUpdate(ctx, inst.TemplateHash, tx)
			if err != nil {
				return err
			}
			if tpl == nil {
				return foundationshared.ErrTemplateNotFound
			}
			refs := breakpointMatcherRefs(tpl.Spec, deps.Executors)
			if err := matcher.Validate(matcher.Matcher(body.Matcher), refs, -1); err != nil {
				return err
			}
			row := persistence.BreakpointRow{
				InstanceID:     inst.ID,
				Matcher:        body.Matcher,
				Checkpoint:     checkpoint,
				SignalType:     body.SignalType,
				Mode:           mode,
				OverflowPolicy: overflowPolicy,
				HitTTLSeconds:  hitTTL,
				TTLSeconds:     body.TTLSeconds,
				CreatedByKey:   requestingKeyID(req.Context()),
			}
			id, err := deps.Persist.Breakpoints().Create(ctx, row, tx)
			if err != nil {
				return err
			}
			bpID = id
			// Re-read to populate created_at / expires_at the DB
			// materialized so the response matches what GET would return.
			fresh, err := deps.Persist.Breakpoints().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			// Defense-in-depth: within this same tx the row is guaranteed
			// to be visible to Get (the INSERT above is in our snapshot;
			// no other transaction can have observed and deleted it yet
			// because the row's id isn't published outside this tx until
			// COMMIT). The nil-check here is a guard against future
			// changes to Get's filter semantics (e.g. an expires_at
			// predicate that filters out rows the test fixture inserts
			// with a backdated timestamp), NOT a real-world concurrency
			// race we have to handle.
			if fresh == nil {
				return fmt.Errorf("breakpoints.create: row %s vanished after insert", id)
			}
			created = *fresh
			return nil
		})
		if txErr != nil {
			writeError(w, txErr)
			return
		}
		_ = bpID // bpID embedded in created.ID
		writeJSON(w, http.StatusCreated, toBreakpointItem(created))
	}
}

// handleListBreakpoints lists the breakpoints active on an instance
// (per spec §4.1; includeExpired=false).
func handleListBreakpoints(deps AppDeps) http.HandlerFunc {
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
		var rows []persistence.BreakpointRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			var err error
			rows, err = deps.Persist.Breakpoints().ListForInstance(ctx, inst.ID, false, tx)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		out := make([]breakpointItem, 0, len(rows))
		for _, r := range rows {
			out = append(out, toBreakpointItem(r))
		}
		writeJSON(w, http.StatusOK, map[string]any{"breakpoints": out})
	}
}

// handleDeleteBreakpoint removes a breakpoint by id; hit rows cascade
// via FK ON DELETE CASCADE. Returns 204 on success, 404 when the
// breakpoint id doesn't exist or doesn't belong to the named instance.
func handleDeleteBreakpoint(deps AppDeps) http.HandlerFunc {
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
		bpID, err := uuid.Parse(chi.URLParam(req, "breakpoint_id"))
		if err != nil {
			badRequest(w, "breakpoint_id must be a UUID")
			return
		}
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			bp, err := deps.Persist.Breakpoints().Get(ctx, bpID, tx)
			if err != nil {
				return err
			}
			if bp == nil || bp.InstanceID != inst.ID {
				return foundationshared.ErrBreakpointNotFound
			}
			return deps.Persist.Breakpoints().Delete(ctx, bpID, tx)
		}); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// resumeBreakpointRequest is the POST body shape for the resume API
// per spec §4.7. The body carries the hit identity (the URL identifies
// the breakpoint; the hit_id selects which of its hits to resume).
type resumeBreakpointRequest struct {
	HitID   string         `json:"hit_id"`
	Overlay map[string]any `json:"overlay,omitempty"`
}

// handleResumeBreakpointHit dispatches to
// runtime.ValidateAndPersistResume and translates the validation /
// not-found sentinels back to HTTP status codes per spec §4.7.
func handleResumeBreakpointHit(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// URL-shape resolution: confirm the named instance and
		// breakpoint exist + belong to each other before doing the
		// resume. Without this check, a typo'd URL would surface as
		// 404 ErrBreakpointHitNotFound (correct in spirit, confusing
		// in practice) when the underlying issue is the breakpoint id.
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, foundationshared.ErrInstanceNotFound.Error())
			return
		}
		bpID, err := uuid.Parse(chi.URLParam(req, "breakpoint_id"))
		if err != nil {
			badRequest(w, "breakpoint_id must be a UUID")
			return
		}
		var body resumeBreakpointRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		hitID, err := uuid.Parse(body.HitID)
		if err != nil {
			badRequest(w, "hit_id must be a UUID")
			return
		}
		// Verify the hit belongs to the named breakpoint (and that the
		// breakpoint belongs to the named instance). Spec §4.7's 404
		// covers "hit_id not in rimsky_breakpoint_hits" and the
		// cascade-delete case; the cross-id check here surfaces typos
		// in the URL with the same 404 shape rather than landing the
		// resume against a hit on a different breakpoint.
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			bp, err := deps.Persist.Breakpoints().Get(ctx, bpID, tx)
			if err != nil {
				return err
			}
			if bp == nil || bp.InstanceID != inst.ID {
				return foundationshared.ErrBreakpointNotFound
			}
			hit, err := deps.Persist.BreakpointHits().Get(ctx, hitID, tx)
			if err != nil {
				return err
			}
			if hit == nil || hit.BreakpointID != bpID {
				return foundationshared.ErrBreakpointHitNotFound
			}
			return nil
		}); err != nil {
			writeError(w, err)
			return
		}
		args := runtime.RunArgs{Persist: deps.Persist, Logger: deps.Logger}
		res, err := runtime.ValidateAndPersistResume(req.Context(), args, hitID, body.Overlay, requestingKeyID(req.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"resumed":      true,
			"first_resume": res.FirstResume,
		})
	}
}

// parseCheckpoint coerces a wire string to the typed constant per spec §4.1.
func parseCheckpoint(s string) (persistence.BreakpointCheckpoint, error) {
	switch s {
	case string(persistence.CheckpointBeforeDispatch):
		return persistence.CheckpointBeforeDispatch, nil
	case string(persistence.CheckpointAfterTerminal):
		return persistence.CheckpointAfterTerminal, nil
	case "":
		return "", fmt.Errorf("checkpoint is required (one of before_dispatch, after_terminal)")
	default:
		return "", fmt.Errorf("checkpoint %q invalid (want before_dispatch or after_terminal)", s)
	}
}

// parseBreakpointMode coerces a wire string to the typed constant.
func parseBreakpointMode(s string) (persistence.BreakpointMode, error) {
	switch s {
	case string(persistence.BreakpointModePause):
		return persistence.BreakpointModePause, nil
	case string(persistence.BreakpointModeNotifyOnly):
		return persistence.BreakpointModeNotifyOnly, nil
	default:
		return "", fmt.Errorf("mode %q invalid (want pause or notify_only)", s)
	}
}

// resolveOverflowPolicy applies the mode-conditional default rules and
// rejects the two invalid combinations per spec §4.8:
//
//   - "" + notify_only → OverflowDropOldest
//   - "" + pause       → OverflowBlockDispatch
//   - pause + drop_oldest → rejected
//   - notify_only + block_dispatch → rejected
func resolveOverflowPolicy(s string, mode persistence.BreakpointMode) (persistence.BreakpointOverflowPolicy, error) {
	var policy persistence.BreakpointOverflowPolicy
	switch s {
	case "":
		switch mode {
		case persistence.BreakpointModeNotifyOnly:
			policy = persistence.OverflowDropOldest
		case persistence.BreakpointModePause:
			policy = persistence.OverflowBlockDispatch
		}
	case string(persistence.OverflowDropOldest):
		policy = persistence.OverflowDropOldest
	case string(persistence.OverflowBlockDispatch):
		policy = persistence.OverflowBlockDispatch
	case string(persistence.OverflowAutoResumeAfterTTL):
		policy = persistence.OverflowAutoResumeAfterTTL
	default:
		return "", fmt.Errorf("overflow_policy %q invalid (want drop_oldest, block_dispatch, or auto_resume_after_ttl)", s)
	}
	// Reject the two illegal combinations per spec §4.8.
	if mode == persistence.BreakpointModePause && policy == persistence.OverflowDropOldest {
		return "", fmt.Errorf("overflow_policy 'drop_oldest' is incompatible with mode 'pause' (pause-mode hits cannot be silently dropped)")
	}
	if mode == persistence.BreakpointModeNotifyOnly && policy == persistence.OverflowBlockDispatch {
		return "", fmt.Errorf("overflow_policy 'block_dispatch' is incompatible with mode 'notify_only' (notify_only is non-blocking by design)")
	}
	return policy, nil
}

// breakpointMatcherRefs builds the matcher.ValidationRefs for a given
// template. NodeTypes and GraphNames are collected from both the legacy
// flat `Nodes:` form and the nested `Graphs:` form so breakpoints work
// on either shape. Executor names come from the operator's
// rimsky.yml-derived AppDeps.Executors. UsedExecutors stays nil
// (breakpoints don't require the executor to be referenced by a
// template node — they can be installed against any executor the
// operator declares, including ones the template doesn't dispatch to,
// to support cross-template debugger habits).
func breakpointMatcherRefs(tpl spec.TemplateSpec, executors map[string]ExecutorEntry) matcher.ValidationRefs {
	nodeTypes := map[string]struct{}{}
	graphNames := map[string]struct{}{spec.MainGraphName: {}}
	for _, n := range tpl.Nodes {
		nodeTypes[n.Type] = struct{}{}
	}
	for _, g := range tpl.Graphs {
		graphNames[g.Name] = struct{}{}
		for _, n := range g.Nodes {
			nodeTypes[n.Type] = struct{}{}
		}
	}
	execNames := make(map[string]struct{}, len(executors))
	for name := range executors {
		execNames[name] = struct{}{}
	}
	return matcher.ValidationRefs{
		NodeTypes:     nodeTypes,
		ExecutorNames: execNames,
		GraphNames:    graphNames,
		// LegacyFlat = false: breakpoints accept "graph: main" on any
		// template regardless of whether it declares sub-graphs.
	}
}

// requestingKeyID returns the API-key ID (UUID string) of the request's
// identity, or "anonymous" when the identity is the anonymous-mode
// synthetic. Used to populate rimsky_instance_breakpoints.created_by_key
// and rimsky_breakpoint_hits.resumed_by_key for the audit trail.
func requestingKeyID(ctx context.Context) string {
	ident, ok := IdentityFromContextOK(ctx)
	if !ok || ident.KeyID == nil {
		return "anonymous"
	}
	return ident.KeyID.String()
}
