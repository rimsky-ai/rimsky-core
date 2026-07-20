// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func registerBreakpointsRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{idOrKey}/breakpoints",
		gate(deps, "breakpoint:create", handleCreateBreakpoint(deps)))
	r.Get("/instances/{idOrKey}/breakpoints",
		gate(deps, "breakpoint:read", handleListBreakpoints(deps)))
	r.Get("/instances/{idOrKey}/breakpoint-hits",
		gate(deps, "breakpoint:read", handleListBreakpointHits(deps)))
	r.Delete("/instances/{idOrKey}/breakpoints/{breakpoint_id}",
		gate(deps, "breakpoint:delete", handleDeleteBreakpoint(deps)))
	r.Post("/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume",
		gate(deps, "breakpoint:resume", handleResumeBreakpointHit(deps)))
}

type createBreakpointRequest struct {
	Matcher        map[string]any `json:"matcher,omitempty"`
	Checkpoint     string         `json:"checkpoint"`
	SignalType     *string        `json:"signal_type,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	OverflowPolicy string         `json:"overflow_policy,omitempty"`
	HitTTLSeconds  *int           `json:"hit_ttl_seconds,omitempty"`
	TTLSeconds     *int           `json:"ttl_seconds,omitempty"`
}

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

		checkpoint, err := parseCheckpoint(body.Checkpoint)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		mode := persistence.BreakpointModePause
		if body.Mode != "" {
			m, err := parseBreakpointMode(body.Mode)
			if err != nil {
				badRequest(w, err.Error())
				return
			}
			mode = m
		}

		overflowPolicy, err := resolveOverflowPolicy(body.OverflowPolicy, mode)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

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

		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
		var created persistence.BreakpointRow
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
			if isDryRun {
				return errDryRunOK
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
				CreatedAt:      deps.Clock.Now(),
			}
			id, err := deps.Persist.Breakpoints().Create(ctx, row, tx)
			if err != nil {
				return err
			}
			fresh, err := deps.Persist.Breakpoints().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			if fresh == nil {
				return fmt.Errorf("breakpoints.create: row %s vanished after insert", id)
			}
			created = *fresh
			return nil
		})
		if isDryRun && errors.Is(txErr, errDryRunOK) {
			summary := map[string]any{
				"instance_id": inst.ID.String(),
				"checkpoint":  string(checkpoint),
				"mode":        string(mode),
			}
			if body.Matcher != nil {
				summary["matcher"] = body.Matcher
			}
			WriteDryRunResponseForced(w, "would_have_created_breakpoint", summary)
			return
		}
		if txErr != nil {
			writeError(w, txErr)
			return
		}
		writeJSON(w, http.StatusCreated, toBreakpointItem(created))
	}
}

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
			rows, err = deps.Persist.Breakpoints().ListForInstance(ctx, inst.ID, false, deps.Clock.Now(), tx)
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

func handleListBreakpointHits(deps AppDeps) http.HandlerFunc {
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
		since, limit, mcpErr := parseSinceLimit(req.URL.Query())
		if mcpErr != nil {
			badRequest(w, mcpErr.Message)
			return
		}
		var hits []persistence.BreakpointHitRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			var err error
			hits, err = deps.Persist.BreakpointHits().ListSinceForInstance(ctx, inst.ID, since, limit+1, tx)
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		truncated := len(hits) > limit
		if truncated {
			hits = hits[:limit]
		}
		items := make([]map[string]any, 0, len(hits))
		for _, h := range hits {
			items = append(items, hitToWireShape(h))
		}
		nextSince := since
		if len(hits) > 0 {
			nextSince = hits[len(hits)-1].Seq
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hits":       items,
			"next_since": nextSince,
			"truncated":  truncated,
		})
	}
}

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
		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			bp, err := deps.Persist.Breakpoints().Get(ctx, bpID, tx)
			if err != nil {
				return err
			}
			if bp == nil || bp.InstanceID != inst.ID {
				return foundationshared.ErrBreakpointNotFound
			}
			if isDryRun {
				return errDryRunOK
			}
			return deps.Persist.Breakpoints().Delete(ctx, bpID, tx)
		}); err != nil {
			if isDryRun && errors.Is(err, errDryRunOK) {
				WriteDryRunResponseForced(w, "would_have_deleted_breakpoint", map[string]any{
					"breakpoint_id": bpID.String(),
				})
				return
			}
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type resumeBreakpointRequest struct {
	HitID   string         `json:"hit_id"`
	Overlay map[string]any `json:"overlay,omitempty"`
}

func handleResumeBreakpointHit(deps AppDeps) http.HandlerFunc {
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
		if WriteDryRunResponse(w, req, "would_have_resumed_breakpoint", map[string]any{
			"hit_id": hitID.String(),
		}) {
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
	if mode == persistence.BreakpointModePause && policy == persistence.OverflowDropOldest {
		return "", fmt.Errorf("overflow_policy 'drop_oldest' is incompatible with mode 'pause' (pause-mode hits cannot be silently dropped)")
	}
	if mode == persistence.BreakpointModeNotifyOnly && policy == persistence.OverflowBlockDispatch {
		return "", fmt.Errorf("overflow_policy 'block_dispatch' is incompatible with mode 'notify_only' (notify_only is non-blocking by design)")
	}
	return policy, nil
}

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
	}
}

func requestingKeyID(ctx context.Context) string {
	ident, ok := IdentityFromContextOK(ctx)
	if !ok || ident.KeyID == nil {
		return auth.AnonymousKeyName
	}
	return ident.KeyID.String()
}
