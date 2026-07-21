// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: executor

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func shouldSpillBlob(args RunArgs, size int) bool {
	return persistence.ShouldSpillBlob(args.Blob, args.BlobSpillThreshold, size)
}

// @concept: executor
func applyTerminalScratchInTx(
	ctx context.Context, args RunArgs, acq *acquisition, scratch []byte, tx persistence.Tx,
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
					"dispatch_id", acq.NodeRunID.String(),
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
	if err := args.Queue.WriteScratch(ctx, acq.NodeRunID, inline, handle, handleBackend, tx); err != nil {
		return fmt.Errorf("applyTerminalScratchInTx: %w", err)
	}
	now := time.Now().UTC()
	if args.Clock != nil {
		now = args.Clock.Now().UTC()
	}
	// @decision: writeback-bumps-progress
	if _, err := args.Queue.BumpLastProgressAt(ctx, acq.NodeRunID, now, tx); err != nil {
		return fmt.Errorf("applyTerminalScratchInTx: bump last_progress_at: %w", err)
	}
	return nil
}
