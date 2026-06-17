// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @constraint: every protocol the host-agent-proxy fronts has a name here so
// the agent can route an inbound dispatch to the right child RPC uniformly —
// the proxy is a transparent forwarder, not a per-protocol special case.
const (
	protocolExecutor       = "executor"
	protocolClaimProducer  = "claim_producer"
	protocolPublisher      = "publisher"
	protocolValidation     = "validation"
	protocolDataProcessing = "data_processing"
)

// handleDispatchFrame relays one inbound DispatchFrame to the live child and
// sends the response frame(s) back on the same stream-id. Errors surface as a
// terminal CANCEL frame so the proxy can translate them into the supervisor-
// facing error_class.
func (a *agent) handleDispatchFrame(ctx context.Context, df *genv1.DispatchFrame) {
	// @constraint: an inbound CANCEL frame is the proxy relaying a supervisor-
	// side cancellation — cancel the matching in-flight dispatch's child stream.
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
	case protocolPublisher, protocolValidation, protocolDataProcessing:
		a.dispatchUnaryByMethod(ctx, child, df)
	default:
		slog.Warn("hostagent: dispatch for unknown protocol", "protocol", df.GetProtocol(), "spawn_id", df.GetSpawnId())
		a.sendDispatchCancel(df)
	}
}

// dispatchExecutor forwards an ExecuteRequest to the child's Executor
// server and sends the returned Outcome back as a single DATA frame on
// the same stream-id. Executor.Execute is unary per
// TD-execute-rpc-unary; the agent is now a one-shot request/response
// relay rather than a stream forwarder.
func (a *agent) dispatchExecutor(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	var req genv1.ExecuteRequest
	if err := proto.Unmarshal(df.GetPayload(), &req); err != nil {
		a.sendDispatchCancel(df)
		return
	}
	// @constraint: a per-dispatch cancelable context is required so an inbound
	// CANCEL frame for this stream_id can tear down the child's inner Execute call.
	dispatchCtx, cancel := context.WithCancel(ctx)
	a.registerDispatchCancel(df.GetStreamId(), cancel)
	defer a.clearDispatchCancel(df.GetStreamId())

	outcome, err := genv1.NewExecutorClient(child.conn).Execute(dispatchCtx, &req)
	if err != nil {
		a.sendDispatchCancel(df)
		return
	}
	payload, marshalErr := proto.Marshal(outcome)
	if marshalErr != nil {
		a.sendDispatchCancel(df)
		return
	}
	a.sendDispatchData(df, payload)
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

// dispatchUnaryByMethod forwards a unary RPC for the non-executor,
// non-claim-producer fronted protocols (publisher / validation /
// data-processing) to the child and sends one response frame back. The
// target RPC is read from the DispatchFrame's rpc_method field — never
// inferred from the payload shape, because these protocols expose multiple
// unary RPCs whose request messages are distinct types (the generic
// analogue of why claim_producer_verb is authoritative for the
// claim-producer path). This is the uniform path that makes the proxy a
// transparent forwarder for every protocol it fronts.
func (a *agent) dispatchUnaryByMethod(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	respBytes, err := forwardUnaryByMethod(ctx, child, df.GetProtocol(), df.GetRpcMethod(), df.GetPayload())
	if err != nil {
		slog.Warn("hostagent: unary dispatch failed",
			"protocol", df.GetProtocol(), "rpc_method", df.GetRpcMethod(), "spawn_id", df.GetSpawnId(), "error", err)
		a.sendDispatchCancel(df)
		return
	}
	a.sendDispatchData(df, respBytes)
}

// forwardUnaryByMethod invokes the rpc_method-named RPC on the child for the
// given protocol and returns the serialized response. rpc_method is
// authoritative: the agent dispatches the RPC the supervisor-facing handler
// actually called rather than guessing from the payload, so a Subscribe is
// never silently delivered as an Unsubscribe (etc.) on a side-effecting
// service.
func forwardUnaryByMethod(ctx context.Context, child *liveChild, protocol, rpcMethod string, payload []byte) ([]byte, error) {
	switch protocol {
	case protocolPublisher:
		return forwardPublisherUnary(ctx, child, rpcMethod, payload)
	case protocolValidation:
		return forwardValidationUnary(ctx, child, rpcMethod, payload)
	case protocolDataProcessing:
		return forwardDataProcessingUnary(ctx, child, rpcMethod, payload)
	default:
		return nil, fmt.Errorf("unsupported unary protocol %q", protocol)
	}
}

// forwardPublisherUnary dispatches one Publisher RPC named by rpcMethod.
func forwardPublisherUnary(ctx context.Context, child *liveChild, rpcMethod string, payload []byte) ([]byte, error) {
	client := genv1.NewPublisherClient(child.conn)
	switch rpcMethod {
	case "Subscribe":
		var req genv1.SubscribeRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal subscribe request: %w", err)
		}
		resp, callErr := client.Subscribe(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	case "Unsubscribe":
		var req genv1.UnsubscribeRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal unsubscribe request: %w", err)
		}
		resp, callErr := client.Unsubscribe(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	default:
		return nil, fmt.Errorf("unsupported publisher rpc_method %q", rpcMethod)
	}
}

// forwardValidationUnary dispatches the single Validation RPC (Validate).
func forwardValidationUnary(ctx context.Context, child *liveChild, rpcMethod string, payload []byte) ([]byte, error) {
	client := genv1.NewValidationClient(child.conn)
	switch rpcMethod {
	case "Validate":
		var req genv1.ValidateRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal validate request: %w", err)
		}
		resp, callErr := client.Validate(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	default:
		return nil, fmt.Errorf("unsupported validation rpc_method %q", rpcMethod)
	}
}

// forwardDataProcessingUnary dispatches one DataProcessing RPC named by
// rpcMethod. BeginCandidate/CommitCandidate/AbandonCandidate request
// messages are distinct types, so rpc_method (not payload shape) selects.
func forwardDataProcessingUnary(ctx context.Context, child *liveChild, rpcMethod string, payload []byte) ([]byte, error) {
	client := genv1.NewDataProcessingClient(child.conn)
	switch rpcMethod {
	case "BeginCandidate":
		var req genv1.BeginCandidateRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal begin-candidate request: %w", err)
		}
		resp, callErr := client.BeginCandidate(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	case "CommitCandidate":
		var req genv1.CommitCandidateRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal commit-candidate request: %w", err)
		}
		resp, callErr := client.CommitCandidate(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	case "AbandonCandidate":
		var req genv1.AbandonCandidateRequest
		if err := proto.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal abandon-candidate request: %w", err)
		}
		resp, callErr := client.AbandonCandidate(ctx, &req)
		if callErr != nil {
			return nil, callErr
		}
		return proto.Marshal(resp)
	default:
		return nil, fmt.Errorf("unsupported data-processing rpc_method %q", rpcMethod)
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
