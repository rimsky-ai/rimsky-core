// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// EventSink is the in-process equivalent of a gRPC server-stream. The
// handler emits ExecuteEvents (heartbeats, named events, and exactly
// one StreamClose) by calling Send. Send returns an error when the
// sink is closed (e.g. supervisor abandoned the dispatch); handlers
// can ignore that error and return — the dispatch loop reaps cleanly.
type EventSink interface {
	Send(*genv1.ExecuteEvent) error
}

// HandlerContext bundles per-dispatch metadata + dependencies the inproc
// handler may need. Threaded through the channel-backed dispatch
// boundary so handlers can call runtime-side helpers (the scratch
// writer; future helpers as the inproc surface grows). Opaque to gRPC
// / HTTP-bridge dispatches.
//
// @concept: executor
type HandlerContext struct {
	Scratch *ScratchWriter
}

// InProcessHandler is the Go interface utility executors implement.
// Shape-matched to Executor.Execute's server-streaming method but
// idiomatic Go: emit events via the sink, return nil on success or an
// error the InProcessClient surfaces as an error terminal.
//
// Handlers MUST emit exactly one StreamClose event and then return.
// Returning without emitting StreamClose, or emitting more than one,
// is a programmer error the dispatch loop reports as
// `stream_closed_without_terminal`.
//
// @concept: executor
type InProcessHandler interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error
}
