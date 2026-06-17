// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Executor-error terminal dispatch. Thin shim that routes the
// terminal-handler's Error verdict directly into the operator's
// `error_types:` chain via applyErrorPolicy. The 4-value action
// vocabulary (pass | give_up | retry | discard_claims_then_retry)
// covers every resolution: `pass` settles the run fresh; `give_up`
// settles the run failed; `retry` and `discard_claims_then_retry`
// re-enqueue. See `runner_error_policy.go::applyErrorPolicy` for the
// policy chain mechanics.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// applyTerminalError routes an executor Error{error_class} terminal
// through the operator's `error_types:` policy chain. Routing is
// uniform per concept:error-policy. The scratch argument is the
// executor-attached opaque bytes from the terminal Error outcome;
// applyErrorPolicyWithScratch persists it BEFORE the retry branch's
// LoadScratchInTx carry-forward so the round-trip stays consistent.
// Tags ride onto the cascading terminal/error/<class> signal's
// payload.tags field so subscribers can `when: "<tag>" in
// payload.tags`-filter the same way they do for terminal/success.
//
// `attributesDel` is the executor's attributes_delta from the settling
// Error outcome (TD-attributes-delta-on-all-settling-terminals). When
// non-empty, the delta is merged against `resolvedAttrs` (the dispatch-
// time substituted attribute view) and the merged object is upserted
// onto the node's attribute row before the policy chain fires. This
// landing makes the delta visible to the retry-branch's successor
// dispatch (attribute carry-forward) and to subscribers gated on
// attribute/<key>/changed signals emitted by the cascading
// terminal/error envelope.
//
//	@concept: error-policy
//	@concept: executor
//	@concept: terminal-tag
//	@concept: attribute
func applyTerminalError(
	ctx context.Context, args RunArgs, acq *acquisition,
	resolvedAttrs map[string]any,
	errorClass string, payload map[string]any, tags []string,
	attributesDel map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	if len(attributesDel) > 0 {
		merged := mergeAttributesDelta(resolvedAttrs, attributesDel)
		if err := upsertFinalAttributesTx(ctx, args, tx, acq, merged); err != nil {
			return nil, fmt.Errorf("applyTerminalError: upsert attributes_delta: %w", err)
		}
	}
	return applyErrorPolicyWithScratch(ctx, args, acq, errorClass, payload, tags, scratch, tx)
}
