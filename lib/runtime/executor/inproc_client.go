// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// HandlerContextFactory builds the per-dispatch HandlerContext from
// typed acquisition identifiers. The dispatch-time inproc glue parses
// the proto request's DispatchId / NodeId fields into typed UUIDs ONCE
// at the Execute entry point — parse failures surface as Execute
// errors, never a silent zero-UUID context, since the supervisor
// populates those proto fields from the acquisition's typed values at
// buildExecuteRequest and a malformed id at this boundary is a runtime
// invariant violation. The supervisor's startup binds this factory to
// a closure over Persist/Queue/Blob/SpillThreshold.
type HandlerContextFactory func(dispatchID, nodeID shared.UUID) HandlerContext

// InProcessClient is a Client implementation that dispatches to an
// InProcessHandler registered in the supervisor's InProcessRegistry.
// The handler runs synchronously on the caller's goroutine and its
// returned *Outcome flows back across the unary boundary — symmetric
// with the gRPC client and the HTTP bridge.
//
// @concept: executor
type InProcessClient struct {
	registry *InProcessRegistry
	url      string
	newHctx  HandlerContextFactory
}

// NewInProcessClient returns a Client backed by an InProcessHandler. The
// newHctx hook lets the supervisor seed per-dispatch HandlerContext
// dependencies (ScratchWriter wired to the dispatch row).
func NewInProcessClient(endpoint Endpoint, registry *InProcessRegistry, newHctx HandlerContextFactory) (Client, error) {
	if endpoint.Transport != "inproc" {
		return nil, fmt.Errorf("executor.NewInProcessClient: transport=%q not inproc", endpoint.Transport)
	}
	if registry == nil {
		return nil, errors.New("executor.NewInProcessClient: registry required")
	}
	if _, ok := registry.Lookup(endpoint.URL); !ok {
		return nil, fmt.Errorf("executor.NewInProcessClient: no handler registered for %q", endpoint.URL)
	}
	return &InProcessClient{registry: registry, url: endpoint.URL, newHctx: newHctx}, nil
}

// Execute drives the in-process handler synchronously on the
// caller's goroutine. The caller's ctx — already deadline-bounded by
// runner_dispatch.go's sync_rpc_deadline wrap (per
// TD-three-dispatch-deadlines) — propagates to the handler unchanged,
// so a handler that honors ctx.Err() picks up the deadline directly.
// Deadline-driven cancellation surfaces as ctx.Err() ==
// context.DeadlineExceeded which the runner's dispatch path maps to
// error_class=executor_sync_timeout. A handler that ignores ctx
// blocks the goroutine; the supervisor's panic-safe wrapper below
// catches any handler panic but cannot interrupt a ctx-deaf handler.
//
// @concept: dispatch-deadlines
func (c *InProcessClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	h, ok := c.registry.Lookup(c.url)
	if !ok {
		return nil, fmt.Errorf("InProcessClient.Execute: no handler for %q", c.url)
	}
	// @constraint: parse the typed UUIDs ONCE at this boundary. The
	// supervisor populates these proto fields from the acquisition's
	// typed values at buildExecuteRequest; a parse failure here is a
	// runtime invariant violation, not user input — surface it as an
	// Execute error rather than a silent zero-UUID HandlerContext.
	dispatchID, err := uuid.Parse(req.DispatchId)
	if err != nil {
		return nil, fmt.Errorf("InProcessClient.Execute: parse dispatch_id %q: %w", req.DispatchId, err)
	}
	nodeID, err := uuid.Parse(req.NodeId)
	if err != nil {
		return nil, fmt.Errorf("InProcessClient.Execute: parse node_id %q: %w", req.NodeId, err)
	}
	hctx := HandlerContext{}
	if c.newHctx != nil {
		hctx = c.newHctx(shared.UUID(dispatchID), shared.UUID(nodeID))
	}
	// @constraint: panic-safe call — a panicking in-process handler must
	// not crash the supervisor (the gRPC analogue only crashes the
	// remote executor process; the in-process model must not be
	// qualitatively less robust). Recover translates a panic into a
	// non-nil error returned to the runtime.
	var outcome *genv1.Outcome
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("inproc handler panic: %v", p)
			}
		}()
		outcome, err = h.Execute(ctx, req, hctx)
	}()
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

func (c *InProcessClient) Close() error { return nil }
