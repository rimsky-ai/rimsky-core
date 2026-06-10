// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Pass-40 acceptance proof for STORY-executor-trace-observability
// (spec:2026-06-08-design-corpus-bootstrap).
//
// User outcome: an operator's dashboard, against a rimsky deployment
// with an executor that advertises trace support, can both (a) subscribe
// to the executor's live trace stream while a dispatched node is in
// flight and receive structured trace events as the executor emits
// them, and (b) after the dispatch terminates, query the trace history
// and receive the full record matching what was streamed.
//
// The delivery surface (per the spec's TD) is the executor's
// ExecutorObservability gRPC surface — `StreamTrace(dispatch_id)` for
// live streaming and `GetTrace(dispatch_id)` for the post-terminal full
// record. Per `concept:executor-observability` the dashboard dials the
// executor's observability endpoint directly; rimsky never proxies
// trace events through the control-api (that's the point of the
// dashboard-direct architecture in the 2026-05-02 design).
//
// This proof boots the full rimsky stack (real scheduler + supervisor +
// control-api + Postgres via testcontainers), wires a trace-advertising
// executor as an `ExtraExecutors` entry, deploys a template that
// references it, creates an instance, drives a real dispatch through
// the supervisor against the executor, and asserts both legs of the
// acceptance through the real gRPC observability surface.
//
// Load-bearing properties (defended explicitly):
//
//   - The trace surface is queried through the REAL gRPC
//     ExecutorObservability surface, dialed externally over the network
//     by a gRPC client the test stands up — not by reaching into the
//     executor's in-memory trace store. This is the spec's stated
//     contract: "the operator-side query is the contract."
//   - The dispatched node reaches the executor via the REAL supervisor
//     dispatch path (the harness's in-process supervisor + scheduler).
//     The executor records the executor-side dispatch_id, the same id
//     the live-stream and history surfaces key on.
//   - Real-time streaming is exhibited by having the executor block
//     mid-dispatch until the test has subscribed via StreamTrace; the
//     events emitted AFTER the test subscribes are received over the
//     wire while the dispatch is still in flight. This rules out the
//     cheaper "subscribe after terminal" shape that would not falsify
//     the stream-silently-drops-events Falsifier.
//   - After terminal, GetTrace's returned event list is asserted equal
//     in length and content (event id, category, message, severity,
//     parent linkage) to the union of events the live subscriber
//     observed — rules out the GetTrace-returns-rows-that-don't-match
//     leg of the Falsifier.

package scenarios

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// traceExecutorName is the executor-name token the template references
// and the harness registers in the resolver. Distinct from the bundled
// "stub" so a regression cannot leak through the default path.
const traceExecutorName = "trace-test-executor"

// traceTestExecutor implements both genv1.ExecutorServer (the dispatch
// surface) and genv1.ExecutorObservabilityServer (the trace surface) on
// one gRPC listener. The executor advertises both supports_trace_get
// and supports_trace_stream so the discovery handshake reports trace
// support; rimsky core does not gate dispatch on those flags, but the
// spec's STORY-executor-trace-observability Falsifier says the trace
// surface must be ABSENT to falsify the story, so we advertise it
// honestly and stand up real implementations behind the flags.
//
// Trace events for a dispatch are kept in a per-dispatch slice with
// per-dispatch subscriber wakeups, modeled on the http-node
// observability server's lock-free-from-the-subscriber's-side design:
// subscribers read events out of the slice at their own cursor woken
// by a coalescing wakeup channel, so AppendEvent never blocks on a
// slow consumer and "events under load" cannot silently drop.
type traceTestExecutor struct {
	genv1.UnimplementedExecutorServer
	genv1.UnimplementedExecutorObservabilityServer

	mu     sync.Mutex
	traces map[string]*traceTestRecord
	subs   map[string]map[*traceTestSub]struct{}

	// gateBefore counts how many events the dispatch handler emits
	// before blocking on releaseGate. The blocking gate is what
	// makes "real-time streaming" falsifiable: a subscriber that
	// attaches after the gate trips will see events that arrive
	// AFTER the subscription, while the dispatch is still in
	// flight. Without the gate the only events visible to
	// StreamTrace would be the snapshot replay, which does not
	// exercise the live-streaming code path on its own.
	gateBefore int

	// dispatchStarted fires (capacity 1) on the first Execute call so
	// the test can read the dispatch_id without polling, and
	// releaseGate is closed by the test to let the dispatch continue
	// past the gate. The test orchestrates these in lockstep with
	// StreamTrace subscription.
	dispatchStarted chan string // sends the dispatch_id once
	releaseGate     chan struct{}
}

// traceTestRecord is one in-memory per-dispatch trace ledger.
type traceTestRecord struct {
	events     []*genv1.TraceEvent
	terminal   bool
	terminalAt time.Time
}

// traceTestSub is one live StreamTrace listener.
type traceTestSub struct {
	wake chan struct{} // capacity 1; coalesces appends
	done chan struct{} // closed when terminal
}

func newTraceTestExecutor() *traceTestExecutor {
	return &traceTestExecutor{
		traces:          map[string]*traceTestRecord{},
		subs:            map[string]map[*traceTestSub]struct{}{},
		gateBefore:      1, // emit one event, gate, emit more, terminal
		dispatchStarted: make(chan string, 1),
		releaseGate:     make(chan struct{}),
	}
}

// Capabilities reports a trace-supporting executor. supports_trace_get
// and supports_trace_stream both true — the Falsifier "trace surface is
// absent for an executor that advertised trace support" tests this.
func (e *traceTestExecutor) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              true,
		SupportsTraceStream:           true,
		RetentionAfterTerminalSeconds: 3600,
		// Permissive open schema so rimsky's dispatch-time
		// expected_attributes_schema gate accepts a node with no
		// attribute config.
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
	}, nil
}

// Execute is the dispatch entry point. The handler captures the
// dispatch_id from the inbound ExecuteRequest (the same id the
// test will pass to StreamTrace / GetTrace), appends one trace
// event, blocks on releaseGate so the test can subscribe BEFORE
// remaining events emit, then emits more events with small
// pauses (to let the subscriber's wakeup channel coalesce naturally)
// before closing with Success. The supervisor terminals the node-run
// on receipt of StreamClose.
func (e *traceTestExecutor) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	dispatchID := req.GetDispatchId()
	if dispatchID == "" {
		return status.Error(codes.InvalidArgument, "dispatch_id required")
	}

	// Register the dispatch so AppendEvent / GetTrace / StreamTrace
	// recognise it (forged ids cannot create records). Modeled on
	// http-node's RegisterDispatch.
	e.registerDispatch(dispatchID)

	// Emit one event before the gate — this is the snapshot the
	// post-subscribe StreamTrace handshake will replay before live
	// streaming begins.
	e.appendEvent(dispatchID, &genv1.TraceEvent{
		EventId:   "ev-start",
		Timestamp: timestamppb.Now(),
		Severity:  genv1.Severity_INFO,
		Category:  "step_started",
		Message:   "dispatch starting",
	})

	// Surface the dispatch_id to the test (non-blocking; only the
	// first Execute call signals — subsequent calls (if any) are
	// dropped on the capacity-1 channel).
	select {
	case e.dispatchStarted <- dispatchID:
	default:
	}

	// Block until the test has subscribed via StreamTrace. The
	// remaining events the test asserts as "live-streamed" emit
	// AFTER this release — exactly what the real-time streaming
	// property requires.
	select {
	case <-e.releaseGate:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}

	// Emit a sequence of trace events post-gate. Small sleeps
	// between emits keep the live subscriber's drain loop honest:
	// the wakeup channel coalesces multiple appends into one drain
	// pass, but with delays each append corresponds to a separate
	// wakeup cycle, exhibiting the live cadence end-to-end.
	for i := 1; i <= 3; i++ {
		e.appendEvent(dispatchID, &genv1.TraceEvent{
			EventId:       fmt.Sprintf("ev-step-%d", i),
			ParentEventId: "ev-start",
			Timestamp:     timestamppb.Now(),
			Severity:      genv1.Severity_INFO,
			Category:      "step_progress",
			Message:       fmt.Sprintf("step %d", i),
		})
		time.Sleep(20 * time.Millisecond)
	}

	// Emit the close-out trace event then mark the trace terminal
	// just before the StreamClose so the live subscriber's "drain
	// then emit trace_complete" loop is exercised correctly.
	e.appendEvent(dispatchID, &genv1.TraceEvent{
		EventId:       "ev-done",
		ParentEventId: "ev-start",
		Timestamp:     timestamppb.Now(),
		Severity:      genv1.Severity_INFO,
		Category:      "step_completed",
		Message:       "dispatch completed",
	})
	e.markTerminal(dispatchID)

	// Send the Success terminal to the supervisor. AttributesDelta
	// nil — the node has no attribute schema beyond the permissive
	// default, so an empty writeback is fine.
	return stream.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{
				Success: &genv1.Success{Changed: true, ChangeSummary: "trace-test"},
			}},
		},
	})
}

// registerDispatch creates the in-memory ledger entry for dispatchID.
// Idempotent — repeat calls keep the same record. Per the http-node
// model: AppendEvent against an unregistered id is a no-op so forged
// ids cannot fill the ledger.
func (e *traceTestExecutor) registerDispatch(dispatchID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.traces[dispatchID]; !ok {
		e.traces[dispatchID] = &traceTestRecord{}
	}
}

// appendEvent records ev on dispatchID's trace and wakes every live
// subscriber via the coalescing wakeup channel. The append happens
// under the same lock that wakes subscribers, so a subscriber that
// observes the wakeup is guaranteed to see the event in its next
// drain — no gap window between append and wake.
//
// Per the stream-silently-drops-events Falsifier: the subscriber
// pump reads directly out of rec.events at its cursor, so multi-event
// bursts that coalesce one wakeup signal still surface every event
// in the next drain pass.
func (e *traceTestExecutor) appendEvent(dispatchID string, ev *genv1.TraceEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.traces[dispatchID]
	if !ok {
		return
	}
	rec.events = append(rec.events, ev)
	for sub := range e.subs[dispatchID] {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

// markTerminal stamps dispatchID's trace terminal and closes every
// live subscriber's done channel so the StreamTrace loop drains the
// tail and emits trace_complete.
func (e *traceTestExecutor) markTerminal(dispatchID string) {
	e.mu.Lock()
	rec, ok := e.traces[dispatchID]
	if !ok || rec.terminal {
		e.mu.Unlock()
		return
	}
	rec.terminal = true
	rec.terminalAt = time.Now()
	subs := e.subs[dispatchID]
	delete(e.subs, dispatchID)
	e.mu.Unlock()
	for sub := range subs {
		close(sub.done)
	}
}

// GetTrace returns the full snapshot for dispatchID. Per the
// http-store-history-doesn't-match-streamed Falsifier the response
// MUST match the events the executor actually emitted; we copy the
// slice under the lock so a concurrent append cannot mutate the
// caller's view mid-iteration.
func (e *traceTestExecutor) GetTrace(_ context.Context, req *genv1.GetTraceRequest) (*genv1.Trace, error) {
	if req.GetDispatchId() == "" {
		return nil, status.Error(codes.InvalidArgument, "dispatch_id required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.traces[req.GetDispatchId()]
	if !ok {
		return &genv1.Trace{DispatchId: req.GetDispatchId(), Evicted: true, Complete: true}, nil
	}
	out := make([]*genv1.TraceEvent, len(rec.events))
	copy(out, rec.events)
	return &genv1.Trace{
		DispatchId: req.GetDispatchId(),
		Complete:   rec.terminal,
		Events:     out,
	}, nil
}

// StreamTrace is the live-subscribe path. Registers a subscriber for
// dispatchID atomically with the cursor (0), then loops draining
// events into the wire stream until either the dispatch terminals
// (drain tail + emit trace_complete and return) or the gRPC stream's
// context cancels (clean exit). Mirrors http-node's design so this
// proof actually exercises a realistic StreamTrace implementation,
// not a degenerate "send and return immediately" stub the spec's
// Falsifier rejects.
func (e *traceTestExecutor) StreamTrace(req *genv1.StreamTraceRequest, stream genv1.ExecutorObservability_StreamTraceServer) error {
	if req.GetDispatchId() == "" {
		return status.Error(codes.InvalidArgument, "dispatch_id required")
	}
	dispatchID := req.GetDispatchId()
	sub, exists := e.subscribe(dispatchID)
	if !exists {
		return stream.Send(traceTestCompleteEvent())
	}
	defer e.unsubscribe(dispatchID, sub)
	cursor := 0
	for {
		events, terminal := e.drainFrom(dispatchID, cursor)
		cursor += len(events)
		for _, ev := range events {
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
		if terminal {
			return stream.Send(traceTestCompleteEvent())
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-sub.done:
			// Drain the tail one more time before closing — events
			// the dispatch emitted between drainFrom and markTerminal
			// must not be dropped (Falsifier: silently drops events
			// under load).
			tail, _ := e.drainFrom(dispatchID, cursor)
			for _, ev := range tail {
				if err := stream.Send(ev); err != nil {
					return err
				}
			}
			return stream.Send(traceTestCompleteEvent())
		case <-sub.wake:
			// Loop back to drain the next batch.
		}
	}
}

func (e *traceTestExecutor) subscribe(dispatchID string) (*traceTestSub, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.traces[dispatchID]; !ok {
		return nil, false
	}
	sub := &traceTestSub{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	if e.subs[dispatchID] == nil {
		e.subs[dispatchID] = map[*traceTestSub]struct{}{}
	}
	e.subs[dispatchID][sub] = struct{}{}
	return sub, true
}

func (e *traceTestExecutor) unsubscribe(dispatchID string, sub *traceTestSub) {
	if sub == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if subs, ok := e.subs[dispatchID]; ok {
		delete(subs, sub)
	}
}

func (e *traceTestExecutor) drainFrom(dispatchID string, cursor int) ([]*genv1.TraceEvent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.traces[dispatchID]
	if !ok {
		return nil, true
	}
	if cursor >= len(rec.events) {
		return nil, rec.terminal
	}
	out := make([]*genv1.TraceEvent, len(rec.events)-cursor)
	copy(out, rec.events[cursor:])
	return out, rec.terminal
}

func traceTestCompleteEvent() *genv1.TraceEvent {
	return &genv1.TraceEvent{
		EventId:   "trace_complete",
		Timestamp: timestamppb.Now(),
		Severity:  genv1.Severity_INFO,
		Category:  "trace_complete",
	}
}

// startTraceExecutor stands up the executor on an OS-assigned gRPC
// listener and returns its endpoint + a handle to the executor so
// the test can read dispatchStarted / close releaseGate. Cleanup is
// registered via t.Cleanup.
func startTraceExecutor(t *testing.T) (*traceTestExecutor, executor.Endpoint) {
	t.Helper()
	exec := newTraceTestExecutor()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen for trace executor")
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, exec)
	genv1.RegisterExecutorObservabilityServer(srv, exec)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
	})
	return exec, executor.Endpoint{Transport: "grpc", URL: lis.Addr().String()}
}

// TestExecutorTraceObservability is the acceptance proof for
// STORY-executor-trace-observability. It drives a real rimsky stack
// + an executor that advertises supports_trace_stream and
// supports_trace_get, and asserts the operator-side dashboard query
// path works: live StreamTrace receives events while the dispatch is
// in flight; post-terminal GetTrace returns the full record matching
// what was streamed.
//
// Falsifier coverage:
//
//   - "Trace stream silently drops events under load" — the
//     post-gate event sequence is asserted received over the wire,
//     in order, with no drops. The executor's append+wake hold the
//     same lock, so any drop would imply a real-time-streaming bug
//     (not a race) and surface here.
//   - "Trace history returns rows that don't correspond to what the
//     executor actually emitted" — GetTrace's returned events are
//     compared event-id-for-event-id with the union of events the
//     live subscriber observed; any synthesis or canned response is
//     caught.
//   - "The trace surface is absent for an executor that advertised
//     trace support" — the test connects via gRPC to the executor's
//     real ExecutorObservability endpoint; an Unimplemented response
//     (the absent-surface shape) would fail StreamTrace immediately
//     with codes.Unimplemented.
func TestExecutorTraceObservability(t *testing.T) {
	t.Parallel()

	traceExec, traceEndpoint := startTraceExecutor(t)
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			traceExecutorName: traceEndpoint,
		},
	})

	// --- Boot the assembled product: register + deploy + instantiate ---
	tid := h.DeployTemplate(node.TemplateSpec{
		Name:                "trace-observability-" + traceExecutorName,
		Version:             "v1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: traceExecutorName,
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-trace-observability", map[string]any{})

	w := h.FindNode(iid, "worker")
	require.NotNil(t, w, "worker node should exist on instance")

	// --- Wait for the real dispatch to reach the executor ---
	// The supervisor enqueues and dispatches; the executor signals
	// dispatchStarted on its first Execute. This is the dispatch_id
	// the operator-side query surfaces will key on.
	var dispatchID string
	select {
	case dispatchID = <-traceExec.dispatchStarted:
	case <-time.After(15 * time.Second):
		t.Fatal("trace executor never received a dispatch from the supervisor")
	}
	require.NotEmpty(t, dispatchID, "executor must record a real dispatch_id")

	// --- Dial the executor's observability surface as a dashboard would ---
	// This is the spec's "operator-side query" — gRPC to the executor's
	// own endpoint, NOT to rimsky's control-api. The Falsifier "trace
	// surface is absent for an executor that advertised trace support"
	// is checked here: an Unimplemented stub would fail at Capabilities
	// or StreamTrace.
	conn, err := grpc.NewClient(traceEndpoint.URL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "dial executor observability endpoint")
	t.Cleanup(func() { _ = conn.Close() })
	obsClient := genv1.NewExecutorObservabilityClient(conn)

	// Verify the executor's discovery handshake honestly advertises
	// trace support. Per the Falsifier, a "surface absent" executor
	// would set these flags false; the spec's STORY-… body requires
	// trace support to be advertised AND queryable.
	caps, err := obsClient.Capabilities(h.Ctx, &genv1.ExecutorCapabilitiesRequest{})
	require.NoError(t, err, "Capabilities RPC")
	require.True(t, caps.GetSupportsTraceGet(), "trace executor must advertise GetTrace")
	require.True(t, caps.GetSupportsTraceStream(), "trace executor must advertise StreamTrace")

	// --- Subscribe to the live stream BEFORE releasing the gate ---
	// This is the load-bearing real-time-streaming property: events
	// emitted by Execute AFTER this subscribe must reach the test
	// over the wire while the dispatch is still in flight. The
	// cheaper shape — subscribe after terminal and read the replay
	// — would not falsify the silently-drops-events-under-load leg
	// of the Falsifier on its own; subscribing before the gate trips
	// makes the live path load-bearing.
	streamCtx, streamCancel := context.WithTimeout(h.Ctx, 20*time.Second)
	defer streamCancel()
	streamClient, err := obsClient.StreamTrace(streamCtx, &genv1.StreamTraceRequest{DispatchId: dispatchID})
	require.NoError(t, err, "StreamTrace open")

	// The replay should deliver the one pre-gate event the
	// executor emitted before signaling dispatchStarted. We do
	// not require a strict snapshot-vs-live boundary because the
	// http-node-style implementation interleaves them — what the
	// spec requires is that EVERY event reaches the subscriber.
	//
	// The receive goroutine appends into streamedEvents from off
	// the test goroutine; the Eventually probe on the main goroutine
	// reads len(streamedEvents). The race detector flags any
	// concurrent read/write on the slice header, so guard both
	// sides with a small mutex. Cross-goroutine readers below take
	// the same mutex to snapshot the slice once streaming closes.
	var (
		streamedMu     sync.Mutex
		streamedEvents = []*genv1.TraceEvent{}
	)
	streamDone := make(chan error, 1)
	go func() {
		for {
			ev, recvErr := streamClient.Recv()
			if recvErr != nil {
				if recvErr == io.EOF {
					streamDone <- nil
					return
				}
				streamDone <- recvErr
				return
			}
			streamedMu.Lock()
			streamedEvents = append(streamedEvents, ev)
			streamedMu.Unlock()
			if ev.GetEventId() == "trace_complete" {
				streamDone <- nil
				return
			}
		}
	}()

	// Drain at least the first (pre-gate) event before releasing
	// the gate. This guarantees the subscriber's cursor advances
	// past the snapshot replay before live events begin, so the
	// post-gate events truly exercise the wakeup-driven live path.
	require.Eventually(t, func() bool {
		streamedMu.Lock()
		defer streamedMu.Unlock()
		return len(streamedEvents) >= 1
	}, 5*time.Second, 25*time.Millisecond, "snapshot replay (ev-start) should reach subscriber before gate release")

	// Release the dispatch — the executor emits ev-step-1..3 +
	// ev-done after this. The live subscriber must observe them
	// before the supervisor sees Success.
	close(traceExec.releaseGate)

	// --- Wait for the live stream to drain and close ---
	select {
	case streamErr := <-streamDone:
		require.NoError(t, streamErr, "StreamTrace should close cleanly with trace_complete")
	case <-time.After(15 * time.Second):
		t.Fatal("StreamTrace never closed; live streaming may have stalled or dropped trace_complete")
	}

	// --- Wait for the node to reach a terminal cascade state ---
	// The supervisor only flips a node to Fresh after the StreamClose
	// terminal — proves the dispatch the trace captures is the same
	// dispatch rimsky terminaled.
	require.True(t,
		h.WaitForNodeState(w.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker node should settle Fresh on Success terminal")

	// --- Live-stream contents: real-time events must include the
	//     post-gate sequence in the order emitted ---
	// Filter out the trace_complete sentinel and assert the event
	// sequence matches what the executor emitted. The Falsifier
	// "trace stream silently drops events under load" fails here
	// if any of ev-step-1..3 / ev-done is missing or out of order.
	streamedMu.Lock()
	streamedSnapshot := make([]*genv1.TraceEvent, len(streamedEvents))
	copy(streamedSnapshot, streamedEvents)
	streamedMu.Unlock()
	streamedIDs := []string{}
	for _, ev := range streamedSnapshot {
		if ev.GetEventId() == "trace_complete" {
			continue
		}
		streamedIDs = append(streamedIDs, ev.GetEventId())
	}
	require.Equal(t,
		[]string{"ev-start", "ev-step-1", "ev-step-2", "ev-step-3", "ev-done"},
		streamedIDs,
		"live stream must deliver every emitted event in order — no drops, no reorder")

	// --- Query the trace history through GetTrace ---
	// Per the Falsifier "trace history returns rows that don't
	// correspond to what the executor actually emitted", we
	// compare the GetTrace event list event-id-for-event-id with
	// the live stream's record.
	getCtx, getCancel := context.WithTimeout(h.Ctx, 5*time.Second)
	defer getCancel()
	trace, err := obsClient.GetTrace(getCtx, &genv1.GetTraceRequest{DispatchId: dispatchID})
	require.NoError(t, err, "GetTrace after terminal")
	require.False(t, trace.GetEvicted(), "trace should not be evicted shortly after terminal")
	require.True(t, trace.GetComplete(), "trace must be marked complete after terminal")

	historyIDs := []string{}
	for _, ev := range trace.GetEvents() {
		historyIDs = append(historyIDs, ev.GetEventId())
	}
	require.Equal(t, streamedIDs, historyIDs,
		"GetTrace history must match the streamed events exactly — same ids, same order")

	// Also assert the structured per-event fields (category, message,
	// severity, parent linkage) round-trip — defends against the
	// "returns rows that don't correspond" Falsifier from a different
	// angle (canned ids but wrong bodies).
	require.Equal(t, "step_started", trace.GetEvents()[0].GetCategory())
	require.Equal(t, "dispatch starting", trace.GetEvents()[0].GetMessage())
	for i := 1; i <= 3; i++ {
		ev := trace.GetEvents()[i]
		require.Equal(t, "step_progress", ev.GetCategory())
		require.Equal(t, fmt.Sprintf("step %d", i), ev.GetMessage())
		require.Equal(t, "ev-start", ev.GetParentEventId(),
			"step events must carry ev-start as parent so dashboard tree-view renders correctly")
		require.Equal(t, genv1.Severity_INFO, ev.GetSeverity())
	}
	require.Equal(t, "step_completed", trace.GetEvents()[4].GetCategory())
}
