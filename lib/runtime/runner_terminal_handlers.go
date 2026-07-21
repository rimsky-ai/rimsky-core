// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: error-policy
// @concept: executor
// @concept: terminal-tag
// @concept: attribute
func applyTerminalError(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any, schema map[string]any,
	errorClass string, payload map[string]any, tags []string,
	attributesDel map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	onSettle := func(ctx context.Context, tx persistence.Tx) error {
		if len(attributesDel) == 0 {
			return nil
		}
		merged := mergeAttributesDelta(resolvedAttrs, attributesDel)
		if err := validateCommitWriteback(ctx, args, acq, schema, merged, tx); err != nil {
			return nil
		}
		if err := upsertFinalAttributesTx(ctx, args, acq, merged, tx); err != nil {
			return fmt.Errorf("applyTerminalError: upsert attributes_delta: %w", err)
		}
		return nil
	}
	return applyErrorPolicyWithScratchAndSettleHook(ctx, args, acq, errorClass, "", payload, tags, attributesDel, scratch, onSettle, tx)
}
