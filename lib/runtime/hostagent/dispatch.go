// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const (
	protocolExecutor       = "executor"
	protocolClaimProducer  = "claim_producer"
	protocolPublisher      = "publisher"
	protocolValidation     = "validation"
	protocolDataProcessing = "data_processing"
)

func (a *agent) handleDispatchFrame(ctx context.Context, df *genv1.DispatchFrame) {
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

func (a *agent) dispatchExecutor(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	var req genv1.ExecuteRequest
	if err := proto.Unmarshal(df.GetPayload(), &req); err != nil {
		a.sendDispatchCancel(df)
		return
	}
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

func (a *agent) dispatchClaimProducer(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	respBytes, err := forwardClaimProducerUnary(ctx, child, df.GetClaimProducerVerb(), df.GetPayload())
	if err != nil {
		slog.Warn("hostagent: claim-producer dispatch failed", "spawn_id", df.GetSpawnId(), "verb", df.GetClaimProducerVerb(), "error", err)
		a.sendDispatchCancel(df)
		return
	}
	a.sendDispatchData(df, respBytes)
}

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

func (a *agent) sendDispatchData(df *genv1.DispatchFrame, payload []byte) bool {
	return a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  df.GetSpawnId(),
		Protocol: df.GetProtocol(),
		Payload:  payload,
		StreamId: df.GetStreamId(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
}

func (a *agent) registerDispatchCancel(streamID string, cancel context.CancelFunc) {
	a.dispatchMu.Lock()
	a.dispatchCancels[streamID] = cancel
	a.dispatchMu.Unlock()
}

func (a *agent) clearDispatchCancel(streamID string) {
	a.dispatchMu.Lock()
	delete(a.dispatchCancels, streamID)
	a.dispatchMu.Unlock()
}

func (a *agent) cancelDispatch(streamID string) {
	a.dispatchMu.Lock()
	cancel, ok := a.dispatchCancels[streamID]
	a.dispatchMu.Unlock()
	if ok {
		cancel()
	}
}

func (a *agent) sendDispatchCancel(df *genv1.DispatchFrame) {
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  df.GetSpawnId(),
		Protocol: df.GetProtocol(),
		StreamId: df.GetStreamId(),
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
	}}})
}
