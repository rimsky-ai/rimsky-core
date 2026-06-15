// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// In-process scratch-writer surface for executor handlers that run
// inside the supervisor process (no gRPC, no HTTP). Provides the same
// inline-vs-spill discipline as the HTTP `/v1/runs/{run_id}/scratch`
// route and the stream-close terminal-scratch persistence, so an
// in-process handler can persist mid-dispatch scratch without
// round-tripping through the callback listener.
//
// The dispatch path constructs a *ScratchWriter per-dispatch and
// threads it into each InProcessHandler invocation via the per-dispatch
// handler context (introduced in a later pass); this file holds only
// the helper type and its Write method.

package executor

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// Logger is an alias for shared.Logger so a future caller wanting to wire
// observability into ScratchWriter (e.g. spill-failure warnings) has a
// stable in-package name.
type Logger = shared.Logger

// ScratchWriter is the in-process equivalent of the §scratch HTTP
// callback route. In-process executor handlers call this directly to
// persist mid-dispatch scratch onto their dispatch row without going
// over the wire. Same spill / threshold behavior as the HTTP-route
// adapter (runtime/callback.go::scratchStoreAdapter).
//
// DispatchID and NodeID are populated per-dispatch by the inproc
// dispatch glue's HandlerContext factory, which threads the
// acquisition's typed UUIDs in directly (no string parse). Until that
// factory lands the type has no live callers and is exercised only by
// the in-package tests — the dispatch site for `InProcessHandler`
// invocations is the only construction point for a ScratchWriter, and
// changes here must stay shape-compatible with that construction site.
//
// @concept: executor
type ScratchWriter struct {
	Persist        persistence.Tables
	Queue          persistence.Queue
	Blob           persistence.BlobBackend
	SpillThreshold int
	DispatchID     shared.UUID
	NodeID         shared.UUID
	// Logger is optional. When set, spill-failure fallbacks are logged at
	// Warn level (matching the HTTP-route adapter's behavior). nil → silent
	// fallback, which preserves the previous behavior for the test fakes
	// that construct a bare-fields ScratchWriter.
	Logger Logger
}

// Write persists scratch bytes onto the dispatch row. Picks inline vs.
// spilled-handle using SpillThreshold + the parked-payload-style
// BlobKey hint. A spill failure falls back to inline with a logged Warn
// (when Logger is set), mirroring the HTTP-route adapter
// (runtime/callback.go::scratchStoreAdapter). The previous behavior —
// returning the spill error to the caller — created a behavioral
// asymmetry where the same payload over the in-process and HTTP paths
// produced different failure modes; this harmonization closes that gap.
//
// **Field-population contract.** Callers MUST construct a ScratchWriter
// by setting every dependency field — Persist, Queue, Blob,
// SpillThreshold, DispatchID, NodeID — and MUST NOT preset any
// related output state (e.g. there is no "inline" or "handle" input
// field on this type; Write makes the inline-vs-spill decision
// internally). The persistence layer's `WriteScratchInTx` is the last-
// line-of-defense mutual-exclusion guard between inline and handle,
// but the chosen pattern here is that the decision lives entirely in
// Write — any future caller that bypasses this constraint will
// surface as a persistence-layer error rather than a clear writer
// error. Today's wiring is fine; this doc-block exists so a future
// in-process caller knows the constraint is implicit.
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
			// @deliberate: spill failure → fall back to inline. Mirrors
			// scratchStoreAdapter (callback.go) and applyTerminalPark.
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
