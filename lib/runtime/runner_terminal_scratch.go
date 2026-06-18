// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: executor

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @concept: executor
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
