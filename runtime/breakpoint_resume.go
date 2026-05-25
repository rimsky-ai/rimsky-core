// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoint_resume.go — runtime-owned validation + persistence for the
// breakpoint-hit resume path. Wraps the persistence Resume call in the
// validation discipline from spec §4.7 (shape check + pre-merge schema
// validation) so any transport surface (HTTP, future MCP webhook, SSE)
// shares one entry point. Per spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md §11
// "separation of concerns".
//
// @concept: breakpoint

package runtime

import (
	"context"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	attributes "github.com/fallguy/rimsky/graph/attribute"
)

// ResumeResult reports whether the resume call was the first one for
// this hit (true) or an idempotent replay (false). Returned to the
// HTTP / MCP transport so they can shape the wire response consistently
// per spec §4.7.
type ResumeResult struct {
	FirstResume bool
}

// ValidateAndPersistResume implements the resume-time validation
// discipline from spec §4.7:
//
//  1. Fetch the hit. Return ErrBreakpointHitNotFound on missing.
//  2. If already resumed, return ResumeResult{FirstResume: false}
//     (idempotent replay — the persistence layer's Resume is itself
//     idempotent; we surface the distinction explicitly).
//  3. Reject overlays on after_terminal hits — the dispatch is already
//     complete, so an overlay can never feed back into the run; the
//     resume path is unconditional notification of the agent. Returns
//     ErrResumeOverlayInvalid with a reason that explains the rule.
//  4. If overlay present (before_dispatch only), merge against
//     hit.Snapshot.dispatch_context.merged_attributes and run
//     graph/attribute.Validate against the snapshot's
//     effective_schema. Return ErrResumeOverlayInvalid on failure.
//  5. Persist the validated overlay + resumed_at + resumed_by_key.
//
// Schema lookup: `lookupEffectiveSchemaForHit` reads
// `snapshot.effective_schema`, which `buildSnapshot` populates from
// `CheckpointContext.EffectiveSchema`. before_dispatch hits always
// carry this (runner_dispatch.go threads `schema` through). When the
// snapshot does not carry it — e.g. a malformed snapshot or a test
// fixture — the resume path logs a Warn and skips overlay schema
// validation, deferring to the supervisor-side defense-in-depth gate
// that re-validates the merged bag before dispatch.
//
// The overlay's top-level shape (it must be a JSON object — the
// persistence-row column is JSONB) is enforced by the HTTP / MCP
// decoder before this function is called.
//
// @concept: breakpoint
func ValidateAndPersistResume(
	ctx context.Context,
	args RunArgs,
	hitID shared.UUID,
	overlay map[string]any,
	byKey string,
) (*ResumeResult, error) {
	var result ResumeResult
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		hit, err := args.Persist.BreakpointHits().Get(ctx, hitID, tx)
		if err != nil {
			return err
		}
		if hit == nil {
			return shared.ErrBreakpointHitNotFound
		}
		if hit.ResumedAt != nil {
			result.FirstResume = false
			return nil
		}
		if overlay != nil {
			// after_terminal hits cannot consume an overlay — the
			// dispatch the breakpoint observed has already committed,
			// so any L6 mutation would silently land in the row but
			// never feed back into the run. Reject explicitly so the
			// agent sees a clear diagnostic instead of having the
			// overlay accepted-and-ignored.
			if hit.Checkpoint == persistence.CheckpointAfterTerminal {
				return shared.Wrap(shared.ErrResumeOverlayInvalid,
					"overlay rejected on after_terminal hit (dispatch is already complete; the overlay can never affect the run)",
					nil)
			}
			// Pull the post-L5 merged bag from the snapshot for the
			// per-spec §4.7 step 2 pre-merge.
			snapDC, _ := hit.Snapshot["dispatch_context"].(map[string]any)
			mergedAttrs, _ := snapDC["merged_attributes"].(map[string]any)
			postOverlay, _ := shared.DeepMergeJSON(mergedAttrs, overlay).(map[string]any)
			if postOverlay == nil {
				postOverlay = map[string]any{}
			}
			schema, schemaOK := lookupEffectiveSchemaForHit(args, hit)
			if schemaOK {
				if vErr := attributes.Validate(schema, postOverlay, attributes.PhaseDispatch); vErr != nil {
					return shared.Wrap(shared.ErrResumeOverlayInvalid, vErr.Error(), nil)
				}
			} else if args.Logger != nil {
				// `buildSnapshot` populates `snapshot.effective_schema`
				// for before_dispatch hits; if it's absent (malformed
				// snapshot or test fixture) the supervisor-side
				// defense-in-depth gate at the blocked runner is the
				// only schema check that fires.
				args.Logger.Warn("breakpoint.resume.schema_not_in_snapshot",
					"hit_id", hitID.String(),
					"reason", "effective_schema absent from snapshot; deferring to supervisor-side defense-in-depth")
			}
		}
		if err := args.Persist.BreakpointHits().Resume(ctx, hitID, byKey, overlay, tx); err != nil {
			return err
		}
		result.FirstResume = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// lookupEffectiveSchemaForHit pulls `snapshot.effective_schema` (a JSON
// object) from the hit row, if present. `buildSnapshot`
// (runtime/breakpoint_eval.go) populates this field at hit-write time
// from `CheckpointContext.EffectiveSchema`. Returns (nil, false) when
// the field is absent — the caller then skips overlay schema
// validation and relies on the supervisor-side defense-in-depth gate
// to catch schema-violating overlays when the blocked runner resumes.
func lookupEffectiveSchemaForHit(_ RunArgs, hit *persistence.BreakpointHitRow) (map[string]any, bool) {
	if hit == nil || hit.Snapshot == nil {
		return nil, false
	}
	raw, ok := hit.Snapshot["effective_schema"]
	if !ok || raw == nil {
		return nil, false
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil, false
	}
	return m, true
}
