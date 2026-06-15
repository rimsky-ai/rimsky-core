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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// applyTerminalError routes an executor Error{error_class} terminal
// through the operator's `error_types:` policy chain. Routing is
// uniform per concept:error-policy. The scratch argument is the
// executor-attached opaque bytes from the terminal Error outcome;
// applyErrorPolicyWithScratch persists it BEFORE the retry branch's
// LoadScratchInTx carry-forward so the round-trip stays consistent.
//
//	@concept: error-policy
//	@concept: executor
func applyTerminalError(
	ctx context.Context, args RunArgs, acq *acquisition,
	errorClass string, payload map[string]any, scratch []byte, tx persistence.Tx,
) (postCommitFn, error) {
	return applyErrorPolicyWithScratch(ctx, args, acq, errorClass, payload, scratch, tx)
}
