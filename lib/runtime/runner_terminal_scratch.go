// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Terminal-path scratch persistence — applies the executor-attached
// scratch bytes from a terminal Success/Error/Park outcome onto the
// dispatch row inside the outer state-mutation tx. Mirrors the
// parked-payload spill discipline in `applyTerminalPark`:
//
//   - Decides inline vs. spilled-handle via `shouldSpillBlob`, which
//     consults `BlobSpillThreshold` and the backend's `Name()` (the
//     degenerate "inline" backend returns false regardless of size).
//   - On spill failure, falls back to inline so a transient backend
//     outage does not fail the terminal. The dispatch's terminal
//     state is the load-bearing write; scratch is executor-private
//     metadata.
//   - When `len(scratch) == 0`, the function returns without issuing
//     a persistence-layer UPDATE — the column already holds whatever
//     scratch state (none, mid-dispatch callback write, or
//     recovery-copied prior) the row had on entry, and an empty
//     terminal scratch is treated as "no terminal-attach" rather than
//     a column reset. This avoids one UPDATE per terminal on the
//     overwhelming majority of dispatches that don't use scratch and
//     removes a race surface where the terminal tx would surface
//     ErrRunRowMissing against a row a concurrent path retired.
//   - Sub-graph exit dispatch rows are EXCLUDED here, in one place,
//     so all three terminal sites (Success, Error/retry-after-error,
//     infra-reenqueue) honor the same "exit's row stays empty"
//     discipline. An exit is internal to the subgraph, not externally
//     addressable, never the target of a recovery enqueue (the parent
//     aggregator picks up the child's terminal via
//     PropagateFromChildState rather than re-dispatching the exit),
//     and its dispatch row is retired in the same tx — so persisting
//     scratch onto an exit's row would be inconsistent with the
//     attribute-row treatment without serving any observable purpose.
//     Centralizing the gate here ensures the Success branch and the
//     Error / Infra branches cannot drift on this rule.
//
// @concept: executor

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// applyTerminalScratchInTx persists `scratch` onto the dispatch row in
// the caller's tx. Picks inline vs. spilled-handle via the BlobBackend's
// per-byte spill threshold. Empty scratch is treated as no
// terminal-attach and short-circuits before any UPDATE: the dispatch
// row's existing scratch state (none, a mid-dispatch callback write,
// or recovery-copied prior bytes) is preserved. Carry-forward to a
// successor still rides through the row's persisted scratch under one
// of the three recovery dispositions — stale-recovery,
// retry-after-error, recalculate — exactly as it would for a non-empty
// terminal scratch.
//
// Sub-graph exit dispatch rows are excluded: see the file header for
// the rationale and the @concept:executor invariant this carve-out
// implements. The carve-out sits here (not at the three call sites)
// so Success, Error/retry-after-error, and Infra terminals stay in
// sync — a future call site reading scratch from a row this function
// updated MUST see the same exit-aware semantics, regardless of which
// terminal flavor wrote it.
func applyTerminalScratchInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq *acquisition, scratch []byte,
) error {
	if len(scratch) == 0 {
		return nil
	}
	if isSubgraphExitNode(acq) {
		return nil
	}
	var (
		inline        []byte
		handle        string
		handleBackend string
	)
	if shouldSpillBlob(args, len(scratch)) {
		key := persistence.BlobKey{
			NodeID: acq.NodeID.String(),
			Hint:   "scratch",
		}
		h, err := args.Blob.Write(ctx, key, scratch)
		if err != nil {
			// @deliberate: spill failure → fall back to inline so the
			// terminal still commits. Mirrors applyTerminalPark's
			// spill-failure fallback.
			if args.Logger != nil {
				args.Logger.Warn("applyTerminalScratchInTx: blob spill failed; falling back to inline",
					"node_id", acq.NodeID.String(),
					"dispatch_id", acq.DispatchID.String(),
					"error", err.Error())
			}
			inline = scratch
		} else {
			handle = string(h)
			handleBackend = args.Blob.Name()
		}
	} else {
		inline = scratch
	}
	if err := args.Queue.WriteScratchInTx(ctx, tx, acq.DispatchID, inline, handle, handleBackend); err != nil {
		return fmt.Errorf("applyTerminalScratchInTx: %w", err)
	}
	return nil
}
