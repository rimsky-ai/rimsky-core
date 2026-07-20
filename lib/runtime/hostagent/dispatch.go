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
	protocolExecutor      = "executor"
	protocolClaimProducer = "claim_producer"
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
		go a.dispatchClaimProducer(ctx, child, df)
	default:
		slog.Warn("hostagent: dispatch for unknown protocol", "protocol", df.GetProtocol(), "spawn_id", df.GetSpawnId())
		a.sendDispatchCancel(df)
	}
}

func (a *agent) dispatchExecutor(ctx context.Context, child *liveChild, df *genv1.DispatchFrame) {
	var req genv1.ExecuteRequest
	if err := proto.Unmarshal(df.GetPayload(), &req); err != nil {
		slog.Warn("hostagent: executor dispatch unmarshal failed", "spawn_id", df.GetSpawnId(), "error", err)
		a.sendDispatchCancel(df)
		return
	}
	dispatchCtx, cancel := context.WithCancel(ctx)
	a.registerDispatchCancel(df.GetStreamId(), cancel)

	go func() {
		defer a.clearDispatchCancel(df.GetStreamId())
		outcome, err := genv1.NewExecutorClient(child.conn).Execute(dispatchCtx, &req)
		if err != nil {
			slog.Warn("hostagent: executor dispatch RPC failed", "spawn_id", df.GetSpawnId(), "error", err)
			a.sendDispatchCancel(df)
			return
		}
		payload, marshalErr := proto.Marshal(outcome)
		if marshalErr != nil {
			slog.Warn("hostagent: executor dispatch outcome marshal failed", "spawn_id", df.GetSpawnId(), "error", marshalErr)
			a.sendDispatchCancel(df)
			return
		}
		a.sendDispatchData(df, payload)
	}()
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

type protoPtr[T any] interface {
	*T
	proto.Message
}

func callUnary[ReqT any, Req protoPtr[ReqT], RespT any, Resp protoPtr[RespT]](ctx context.Context, payload []byte, call func(context.Context, Req) (Resp, error)) ([]byte, error) {
	var reqZero ReqT
	req := Req(&reqZero)
	if err := proto.Unmarshal(payload, req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	resp, err := call(ctx, req)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(resp)
}

func forwardClaimProducerUnary(ctx context.Context, child *liveChild, verb genv1.DispatchFrame_ClaimProducerVerb, payload []byte) ([]byte, error) {
	client := genv1.NewClaimProducerClient(child.conn)

	switch verb {
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN:
		return callUnary[genv1.OpenRequest](ctx, payload, func(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			return client.Open(ctx, req)
		})
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_COMMIT:
		return callUnary[genv1.CommitRequest](ctx, payload, func(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
			return client.Commit(ctx, req)
		})
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_ABANDON:
		return callUnary[genv1.AbandonRequest](ctx, payload, func(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
			return client.Abandon(ctx, req)
		})
	case genv1.DispatchFrame_CLAIM_PRODUCER_VERB_RELEASE:
		return callUnary[genv1.ReleaseRequest](ctx, payload, func(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
			return client.Release(ctx, req)
		})
	default:
		return nil, fmt.Errorf("unspecified claim-producer verb %v", verb)
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
