// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
)

type ResumeResult struct {
	FirstResume bool
}

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
			if hit.Checkpoint == persistence.CheckpointAfterTerminal {
				return shared.Wrap(shared.ErrResumeOverlayInvalid,
					"overlay rejected on after_terminal hit (dispatch is already complete; the overlay can never affect the run)",
					nil)
			}
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
