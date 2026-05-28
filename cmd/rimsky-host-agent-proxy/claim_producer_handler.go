// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claim_producer_handler.go — the supervisor-facing ClaimProducer
// protocol handler. Open resolves (owner → agent → binding) and lazily
// spawns the producer (expected_protocols: [claim_producer]), forwards
// the unary RPC via a DispatchFrame, and awaits one response frame.
// Commit/Abandon/Release route to the same spawned producer via the
// claim-id → spawn route recorded at Open. Proxy-side failures surface as
// gRPC error statuses carrying the error_class in a google.rpc.ErrorInfo
// detail (the shape the supervisor's claim-producer client decodes).
//
// @concept: host-agent-proxy

package main

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const protocolClaimProducer = "claim_producer"

// claimProducerHandler implements genv1.ClaimProducerServer.
type claimProducerHandler struct {
	genv1.UnimplementedClaimProducerServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
	callTimeout  time.Duration
}

func newClaimProducerHandler(state *proxyState, cfg Config) *claimProducerHandler {
	return &claimProducerHandler{
		state:        state,
		fetch:        newControlAPIFetcher(&http.Client{Timeout: 10 * time.Second}, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
		callTimeout:  60 * time.Second,
	}
}

// Capabilities advertises the full write-semantics envelope. The proxy is
// transport — per-claim realized semantics come from each spawned
// producer's Open response, so the proxy advertises all four values and
// does not narrow the envelope.
func (h *claimProducerHandler) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY,
		},
		SupportsSplitScope:     true,
		SupportsScopesConflict: false,
	}, nil
}

// Open resolves+spawns the producer and forwards the unary Open RPC.
func (h *claimProducerHandler) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	res, rerr := resolveAndSpawn(
		ctx, h.state, h.fetch,
		[]string{protocolClaimProducer},
		req.GetInstanceId(),
		"", // claim-producer has no callback URL to rewrite
		h.spawnTimeout,
	)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}

	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal open: " + err.Error()})
	}

	respBytes, rerr := forwardUnary(ctx, res.agent, res.spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}

	var resp genv1.OpenResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal open response: " + err.Error()})
	}

	// Record the claim route so Commit/Abandon/Release (which carry only a
	// claim_id) route back to this spawned producer.
	h.state.recordClaimRoute(req.GetClaimId(), res.agent.apiKeyID, res.spawnID)
	return &resp, nil
}

// Commit forwards the unary Commit RPC to the producer holding the claim.
func (h *claimProducerHandler) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	agent, spawnID, rerr := h.routeByClaim(req.GetClaimId())
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal commit: " + err.Error()})
	}
	respBytes, rerr := forwardUnary(ctx, agent, spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_COMMIT, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	var resp genv1.CommitResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal commit response: " + err.Error()})
	}
	return &resp, nil
}

// Abandon forwards the unary Abandon RPC to the producer holding the claim.
func (h *claimProducerHandler) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	agent, spawnID, rerr := h.routeByClaim(req.GetClaimId())
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal abandon: " + err.Error()})
	}
	respBytes, rerr := forwardUnary(ctx, agent, spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_ABANDON, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	var resp genv1.AbandonResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal abandon response: " + err.Error()})
	}
	return &resp, nil
}

// Release forwards the unary Release RPC and forgets the claim route.
func (h *claimProducerHandler) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	agent, spawnID, rerr := h.routeByClaim(req.GetClaimId())
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal release: " + err.Error()})
	}
	respBytes, rerr := forwardUnary(ctx, agent, spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_RELEASE, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	var resp genv1.ReleaseResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal release response: " + err.Error()})
	}
	h.state.dropClaimRoute(req.GetClaimId())
	return &resp, nil
}

// routeByClaim resolves the live agent + spawn for an existing claim.
func (h *claimProducerHandler) routeByClaim(claimID string) (*agentConnection, string, *resolveError) {
	route, ok := h.state.lookupClaimRoute(claimID)
	if !ok {
		return nil, "", &resolveError{class: errClassBindingNotFound, msg: "no claim route for claim " + claimID}
	}
	agent, ok := h.state.lookupAgent(route.apiKeyID)
	if !ok {
		return nil, "", &resolveError{class: errClassHostAgentDisconnected, msg: "agent gone for claim " + claimID}
	}
	if _, ok := h.state.lookupSpawn(route.spawnID); !ok {
		return nil, "", &resolveError{class: errClassHostAgentDisconnected, msg: "spawn gone for claim " + claimID}
	}
	return agent, route.spawnID, nil
}

// forwardUnary tunnels a serialized unary request to the spawned child
// over a fresh dispatch stream and awaits exactly one response frame. The
// verb names which ClaimProducer RPC the agent must invoke on the child —
// it rides the wire because Commit/Abandon/Release are byte-identical at
// claim_id and the agent cannot infer the verb from the payload shape.
func forwardUnary(ctx context.Context, agent *agentConnection, spawnID string, payload []byte, verb genv1.DispatchFrame_ClaimProducerVerb, timeout time.Duration) ([]byte, *resolveError) {
	streamID := uuid.NewString()
	respCh := agent.registerStream(streamID)
	defer agent.clearStream(streamID)

	if !agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:           spawnID,
		Protocol:          protocolClaimProducer,
		Payload:           payload,
		StreamId:          streamID,
		Kind:              genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		ClaimProducerVerb: verb,
	}}}) {
		return nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected before dispatch"}
	}

	select {
	case frame, ok := <-respCh:
		if !ok {
			return nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected mid-call"}
		}
		if frame.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
			return nil, &resolveError{class: errClassExecutorCrashed, msg: "spawned producer cancelled the call"}
		}
		return frame.GetPayload(), nil
	case <-time.After(timeout):
		return nil, &resolveError{class: errClassExecutorCrashed, msg: "producer call timed out"}
	case <-ctx.Done():
		return nil, &resolveError{class: errClassExecutorCrashed, msg: "caller context cancelled: " + ctx.Err().Error()}
	case <-agent.closed:
		return nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected mid-call"}
	}
}

// claimProducerStatus maps a resolveError to a gRPC status carrying the
// error_class in a google.rpc.ErrorInfo detail. Missing-binding-style
// faults use FailedPrecondition; all other proxy-side faults use Internal
// (the shape the supervisor's claim-producer client decodes).
func claimProducerStatus(rerr *resolveError) error {
	code := codes.Internal
	if rerr.class == errClassBindingNotFound {
		code = codes.FailedPrecondition
	}
	st := status.New(code, rerr.Error())
	withInfo, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: rerr.class,
		Domain: "rimsky.host-agent-proxy",
	})
	if err != nil {
		return st.Err()
	}
	return withInfo.Err()
}

// claimProducerObsHandler implements genv1.ClaimProducerObservabilityServer.
// The proxy advertises a minimal observability envelope — per-claim
// detail comes from each spawned producer.
type claimProducerObsHandler struct {
	genv1.UnimplementedClaimProducerObservabilityServer
}

func newClaimProducerObsHandler() *claimProducerObsHandler { return &claimProducerObsHandler{} }

func (h *claimProducerObsHandler) Capabilities(_ context.Context, _ *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return &genv1.ClaimProducerObservabilityCapabilities{}, nil
}
