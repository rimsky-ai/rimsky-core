// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"

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
// The handler runs on a goroutine; events flow through a buffered
// channel-backed EventStream. The dispatch loop's Recv / Close
// semantics are identical to the gRPC client.
//
// @concept: executor
type InProcessClient struct {
	registry *InProcessRegistry
	url      string // inproc executor URL, e.g. "inproc://loop_counter"
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

func (c *InProcessClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error) {
	h, ok := c.registry.Lookup(c.url)
	if !ok {
		return nil, fmt.Errorf("InProcessClient.Execute: no handler for %q", c.url)
	}
	// Parse the typed UUIDs ONCE at this boundary. The supervisor
	// populates these proto fields from the acquisition's typed values
	// at buildExecuteRequest; a parse failure here is a runtime
	// invariant violation, not user input — surface it as an Execute
	// error rather than a silent zero-UUID HandlerContext.
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
	// Derive an internal cancellable context so EventStream.Close /
	// abandoning the dispatch mid-stream can signal the handler
	// goroutine to drop its in-flight Send and exit promptly. Without
	// this, a handler whose Send blocks on a full buffer after the
	// supervisor abandoned the stream would leak the goroutine for
	// the supervisor's lifetime (the gRPC analogue cancels the
	// server-stream when the client goes away; we mirror it).
	handlerCtx, cancel := context.WithCancel(ctx)
	// Buffered channel + close-on-handler-return is the EOF protocol.
	// Buffer of 16 covers heartbeat/named-event bursts without blocking
	// typical handler loops; a deeper buffer would mask handler bugs
	// (the dispatch loop is supposed to drain at gRPC-stream cadence).
	ch := make(chan *genv1.ExecuteEvent, 16)
	errCh := make(chan error, 1)
	sink := &channelSink{ch: ch, ctx: handlerCtx}
	go func() {
		// Panic-safe goroutine: a panicking handler must NOT crash the
		// supervisor (the gRPC analogue only crashes the remote executor
		// process; the inproc model must not be qualitatively less
		// robust). The recover deferred translates a panic into a
		// non-nil error written to errCh, and `close(errCh)` runs from
		// the deferred so both panic and clean-return paths close errCh
		// — without that, a panic would close ch but leak errCh, and
		// inprocEventStream.Recv would wedge forever waiting on a
		// channel that is never closed and never sent to.
		defer func() {
			if p := recover(); p != nil {
				select {
				case errCh <- fmt.Errorf("inproc handler panic: %v", p):
				default:
				}
			}
			close(errCh)
		}()
		defer close(ch)
		if err := h.Execute(handlerCtx, req, sink, hctx); err != nil {
			errCh <- err
		}
	}()
	return &inprocEventStream{ch: ch, errCh: errCh, cancel: cancel}, nil
}

func (c *InProcessClient) Close() error { return nil }

// channelSink is the in-process EventSink. The handler emits
// ExecuteEvents through it, blocking on a full buffer until the
// dispatch loop drains — identical to a gRPC server-stream's Send
// blocking on backpressure. When the dispatch loop abandons the
// stream (EventStream.Close), the embedded ctx cancels and a blocked
// Send returns ctx.Err() so the handler goroutine exits rather than
// leaking. The handler is expected to surrender on a Send error
// (return promptly) — the InProcessHandler doc-block spells this
// contract out.
type channelSink struct {
	ch  chan<- *genv1.ExecuteEvent
	ctx context.Context
}

func (s *channelSink) Send(ev *genv1.ExecuteEvent) error {
	select {
	case s.ch <- ev:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

type inprocEventStream struct {
	ch     <-chan *genv1.ExecuteEvent
	errCh  <-chan error
	cancel context.CancelFunc
}

func (s *inprocEventStream) Recv() (*genv1.ExecuteEvent, error) {
	ev, ok := <-s.ch
	if !ok {
		// Channel closed — handler returned. If the handler returned an
		// error, surface it; otherwise EOF.
		if err, ok := <-s.errCh; ok && err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return ev, nil
}

// Close cancels the handler's derived context so a blocked Send wakes
// and returns ctx.Err(). The handler goroutine is then expected to
// exit promptly; channelSink.Send's ctx-cancel branch is the wake
// path. Idempotent: calling Close multiple times is safe (the
// CancelFunc itself is idempotent).
func (s *inprocEventStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
