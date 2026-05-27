// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// dispatch.go — relays a DispatchFrame to the spawned child's local gRPC
// server and streams the response(s) back on the same stream-id. Executor
// dispatch is server-streaming: the agent opens Execute, sends the request,
// and forwards each ExecuteEvent as a DATA DispatchFrame, terminating on the
// inner StreamClose. Claim-producer dispatch is unary: the agent forwards one
// request and sends one response frame. The wire DispatchFrame carries the
// claim_producer_verb naming which ClaimProducer RPC to invoke on the child —
// CommitRequest/AbandonRequest/ReleaseRequest are byte-identical at claim_id,
// so the agent must NOT infer the verb from the payload shape (doing so
// silently commits an Abandon/Release, a state-integrity bug).
//
// @concept: host-agent
package hostagent

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// Protocol names the proxy stamps on a DispatchFrame at dispatch start.
const (
	protocolExecutor      = "executor"
	protocolClaimProducer = "claim_producer"
)

// handleDispatchFrame relays one inbound DispatchFrame to the live child and
// sends the response frame(s) back on the same stream-id. Errors surface as a
// terminal CANCEL frame so the proxy can translate them into the supervisor-
// facing error_class.
func (a *agent) handleDispatchFrame(ctx context.Context, df *genv1.DispatchFrame) {
	// An inbound CANCEL frame is the proxy relaying a supervisor-side
	// cancellation: cancel the matching in-flight dispatch's child stream.
	// It is not a fresh dispatch, so there is no child RPC to start here.
	if df.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		a.cancelDispatch(df.GetStreamId())
		return
	}

	a.childMu.Lock()
	child, ok := a.children[df.GetSpawnId()]
	a.childMu.Unlock()
	if !ok {
		a.sendDispatchCancel(df)
		return
	}

	switch df.GetProtocol() {
	case protocolExecutor:
		a.dispatchExecutor(ctx, child, df)
	case protocolClaimProducer:
		a.dispatchClaimProducer(ctx, child, df)
	default:
		slog.Warn("hostagent: dispatch for unknown protocol", "protocol", df.GetProtocol(), "spawn_id", df.GetSpawnId())
		a.sendDispatchCancel(df)
	}
}

// dispatchExecutor forwards an ExecuteRequest to the child's Executor server
// and streams each ExecuteEvent back as a DATA frame on the same stream-id.
func (a *agent) dispatchExecutor(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	var req genv1.ExecuteRequest
	if err := proto.Unmarshal(df.GetPayload(), &req); err != nil {
		a.sendDispatchCancel(df)
		return
	}

	// Register a per-dispatch cancelable context so an inbound CANCEL frame
	// for this stream_id tears down the child's inner Execute stream.
	dispatchCtx, cancel := context.WithCancel(ctx)
	a.registerDispatchCancel(df.GetStreamId(), cancel)
	defer a.clearDispatchCancel(df.GetStreamId())

	stream, err := genv1.NewExecutorClient(child.conn).Execute(dispatchCtx, &req)
	if err != nil {
		a.sendDispatchCancel(df)
		return
	}

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			// EOF or transport error: the inner StreamClose (if any) was
			// already forwarded below, so an error here means the child
			// dropped without a terminal. Signal CANCEL.
			a.sendDispatchCancel(df)
			return
		}
		payload, marshalErr := proto.Marshal(ev)
		if marshalErr != nil {
			a.sendDispatchCancel(df)
			return
		}
		if !a.sendDispatchData(df, payload) {
			return // stream torn down
		}
		if _, terminal := ev.GetEvent().(*genv1.ExecuteEvent_StreamClose); terminal {
			return // forwarded the inner terminal; the proxy closes its side.
		}
	}
}

// dispatchClaimProducer forwards a unary claim-producer RPC to the child and
// sends one response frame back. The verb is read from the DispatchFrame's
// claim_producer_verb field — never inferred from the payload shape, because
// Commit/Abandon/Release are byte-identical at claim_id.
func (a *agent) dispatchClaimProducer(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	respBytes, err := forwardClaimProducerUnary(ctx, child, df.GetClaimProducerVerb(), df.GetPayload())
	if err != nil {
		slog.Warn("hostagent: claim-producer dispatch failed", "spawn_id", df.GetSpawnId(), "verb", df.GetClaimProducerVerb(), "error", err)
		a.sendDispatchCancel(df)
		return
	}
	a.sendDispatchData(df, respBytes)
}

// forwardClaimProducerUnary invokes the verb-named ClaimProducer RPC on the
// child and returns the serialized response. The verb is authoritative: the
// request messages for Commit/Abandon/Release are wire-identical at claim_id,
// so the agent must dispatch the RPC the supervisor actually called rather
// than guess from the payload.
func forwardClaimProducerUnary(ctx context.Context, child *liveChild, verb genv1.DispatchFrame_ClaimProducerVerb, payload []byte) ([]byte, error) {
	client := genv1.NewClaimProducerClient(child.conn)

	switch verb {
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN:
		var req genv1.OpenRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal open request: %w", err)
		}
		resp, callErr := client.Open(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_COMMIT:
		var req genv1.CommitRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal commit request: %w", err)
		}
		resp, callErr := client.Commit(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_ABANDON:
		var req genv1.AbandonRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal abandon request: %w", err)
		}
		resp, callErr := client.Abandon(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_RELEASE:
		var req genv1.ReleaseRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal release request: %w", err)
		}
		resp, callErr := client.Release(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	default:
		return nil, fmt.Errorf("unspecified claim-producer verb %v", verb)
	}
}

// sendDispatchData sends a DATA DispatchFrame back to the proxy on the
// originating stream-id. Returns false if the stream is torn down.
func (a *agent) sendDispatchData(df *genv1.DispatchFrame, payload []byte) bool {
	return a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  df.GetSpawnId(),
		Protocol: df.GetProtocol(),
		Payload:  payload,
		StreamId: df.GetStreamId(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
}

// registerDispatchCancel records the cancel func for an in-flight dispatch
// stream so an inbound CANCEL frame can tear it down.
func (a *agent) registerDispatchCancel(streamID string, cancel context.CancelFunc) {
	a.dispatchMu.Lock()
	a.dispatchCancels[streamID] = cancel
	a.dispatchMu.Unlock()
}

// clearDispatchCancel removes a dispatch stream's cancel func after the
// dispatch completes.
func (a *agent) clearDispatchCancel(streamID string) {
	a.dispatchMu.Lock()
	delete(a.dispatchCancels, streamID)
	a.dispatchMu.Unlock()
}

// cancelDispatch cancels the in-flight dispatch for streamID, if any.
func (a *agent) cancelDispatch(streamID string) {
	a.dispatchMu.Lock()
	cancel, ok := a.dispatchCancels[streamID]
	a.dispatchMu.Unlock()
	if ok {
		cancel()
	}
}

// sendDispatchCancel sends a terminal CANCEL DispatchFrame so the proxy can
// translate the failure into the supervisor-facing error_class.
func (a *agent) sendDispatchCancel(df *genv1.DispatchFrame) {
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  df.GetSpawnId(),
		Protocol: df.GetProtocol(),
		StreamId: df.GetStreamId(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
	}}})
}
