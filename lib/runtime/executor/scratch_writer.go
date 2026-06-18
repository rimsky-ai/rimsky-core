// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type Logger = shared.Logger

// @concept: executor
type ScratchWriter struct {
	Persist        persistence.Tables
	Queue          persistence.Queue
	Blob           persistence.BlobBackend
	SpillThreshold int
	DispatchID     shared.UUID
	NodeID         shared.UUID
	Logger         Logger
}

func (w *ScratchWriter) Write(ctx context.Context, bytes []byte) error {
	var (
		inline        []byte
		handle        string
		handleBackend string
	)
	if w.Blob != nil && persistence.ShouldSpillBlob(w.Blob, w.SpillThreshold, len(bytes)) {
		key := persistence.BlobKey{NodeID: w.NodeID.String(), Hint: "scratch"}
		h, err := w.Blob.Write(ctx, key, bytes)
		if err != nil {
			if w.Logger != nil {
				w.Logger.Warn("ScratchWriter: blob spill failed; falling back to inline",
					"dispatch_id", w.DispatchID.String(),
					"node_id", w.NodeID.String(),
					"error", err.Error())
			}
			inline = bytes
		} else {
			handle = string(h)
			handleBackend = w.Blob.Name()
		}
	} else {
		inline = bytes
	}
	return w.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return w.Queue.WriteScratchInTx(ctx, tx, w.DispatchID, inline, handle, handleBackend)
	})
}
