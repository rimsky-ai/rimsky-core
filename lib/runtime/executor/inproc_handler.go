// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// HandlerContext bundles per-dispatch metadata + dependencies the
// in-process handler may need. Threaded across the in-process dispatch
// boundary so handlers can call runtime-side helpers (the scratch
// writer; future helpers as the in-process surface grows). Opaque to
// gRPC / HTTP-bridge dispatches.
//
// @concept: executor
type HandlerContext struct {
	Scratch *ScratchWriter
}

// InProcessHandler is the Go interface utility executors implement.
// Shape-matched to the unary Executor.Execute RPC: the handler runs
// synchronously and returns the settling Outcome (one of Success /
// Error / Park / AwaitAsyncCallback). The in-process client surfaces
// a non-nil err as a synthetic Error outcome.
//
// Per concept:executor / TD-execute-rpc-unary the streaming event
// surface (Heartbeat / NamedEvent / StreamClose) is gone — the only
// boundary the in-process handler crosses is a single return value
// and at most a scratch-writer callback during the synchronous run.
//
// @concept: executor
type InProcessHandler interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error)
}
