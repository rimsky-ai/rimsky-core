// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy

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

type claimProducerHandler struct {
	genv1.UnimplementedClaimProducerServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
	callTimeout  time.Duration
}

func newClaimProducerHandler(state *proxyState, cfg Config, controlAPIClient *http.Client) *claimProducerHandler {
	return &claimProducerHandler{
		state:        state,
		fetch:        newControlAPIFetcher(controlAPIClient, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
		callTimeout:  60 * time.Second,
	}
}

func (h *claimProducerHandler) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC,
			genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY,
		},
		SupportsSplitScope:     false,
		SupportsScopesConflict: false,
	}, nil
}

func (h *claimProducerHandler) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	res, rerr := resolveAndSpawn(
		ctx, h.state, h.fetch,
		[]string{protocolClaimProducer},
		req.GetInstanceId(),
		req.GetRunScopeId(),
		"",
		h.spawnTimeout,
	)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}

	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal open: " + err.Error()})
	}

	respBytes, rerr := forwardUnary(ctx, res.daemon, res.spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}

	var resp genv1.OpenResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal open response: " + err.Error()})
	}

	h.state.recordClaimRoute(req.GetClaimId(), res.daemon.routingIdentity, res.spawnID)
	return &resp, nil
}

func (h *claimProducerHandler) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	daemon, spawnID, rerr := h.routeByClaim(req.GetClaimId())
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal commit: " + err.Error()})
	}
	respBytes, rerr := forwardUnary(ctx, daemon, spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_COMMIT, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	var resp genv1.CommitResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal commit response: " + err.Error()})
	}
	return &resp, nil
}

func (h *claimProducerHandler) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	daemon, spawnID, rerr := h.routeByClaim(req.GetClaimId())
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal abandon: " + err.Error()})
	}
	respBytes, rerr := forwardUnary(ctx, daemon, spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_ABANDON, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	var resp genv1.AbandonResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal abandon response: " + err.Error()})
	}
	return &resp, nil
}

func (h *claimProducerHandler) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	daemon, spawnID, rerr := h.routeByClaim(req.GetClaimId())
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal release: " + err.Error()})
	}
	respBytes, rerr := forwardUnary(ctx, daemon, spawnID, payload, genv1.DispatchFrame_CLAIM_PRODUCER_VERB_RELEASE, h.callTimeout)
	if rerr != nil {
		return nil, claimProducerStatus(rerr)
	}
	var resp genv1.ReleaseResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, claimProducerStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal release response: " + err.Error()})
	}
	return &resp, nil
}

func (h *claimProducerHandler) routeByClaim(claimID string) (*daemonConnection, string, *resolveError) {
	route, ok := h.state.lookupClaimRoute(claimID)
	if !ok {
		return nil, "", &resolveError{class: errClassBindingNotFound, msg: "no claim route for claim " + claimID}
	}
	daemon, ok := h.state.lookupDaemon(route.routingIdentity)
	if !ok {
		return nil, "", &resolveError{class: errClassHostDaemonDisconnected, msg: "daemon gone for claim " + claimID}
	}
	if _, ok := h.state.lookupSpawn(route.spawnID); !ok {
		return nil, "", &resolveError{class: errClassHostDaemonDisconnected, msg: "spawn gone for claim " + claimID}
	}
	return daemon, route.spawnID, nil
}

func forwardUnary(ctx context.Context, daemon *daemonConnection, spawnID string, payload []byte, verb genv1.DispatchFrame_ClaimProducerVerb, timeout time.Duration) ([]byte, *resolveError) {
	streamID := uuid.NewString()
	respCh := daemon.registerStream(streamID)
	defer daemon.clearStream(streamID)

	if !sendDispatchFrame(daemon, spawnID, protocolClaimProducer, streamID, payload, &verb) {
		return nil, &resolveError{class: errClassHostDaemonDisconnected, msg: "daemon disconnected before dispatch"}
	}

	select {
	case frame, ok := <-respCh:
		if !ok {
			return nil, &resolveError{class: errClassHostDaemonDisconnected, msg: "daemon disconnected mid-call"}
		}
		if frame.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
			return nil, &resolveError{class: errClassExecutorCrashed, msg: "spawned producer cancelled the call"}
		}
		return frame.GetPayload(), nil
	case <-time.After(timeout):
		return nil, &resolveError{class: errClassExecutorCrashed, msg: "producer call timed out"}
	case <-ctx.Done():
		return nil, &resolveError{class: errClassExecutorCrashed, msg: "caller context cancelled: " + ctx.Err().Error()}
	case <-daemon.closed:
		return nil, &resolveError{class: errClassHostDaemonDisconnected, msg: "daemon disconnected mid-call"}
	}
}

func claimProducerStatus(rerr *resolveError) error {
	code := codes.Internal
	if rerr.class == errClassBindingNotFound {
		code = codes.FailedPrecondition
	}
	st := status.New(code, rerr.Error())
	withInfo, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: rerr.class,
		Domain: "rimsky.host-daemon-proxy",
	})
	if err != nil {
		return st.Err()
	}
	return withInfo.Err()
}

type claimProducerObsHandler struct {
	genv1.UnimplementedClaimProducerObservabilityServer
}

func newClaimProducerObsHandler() *claimProducerObsHandler { return &claimProducerObsHandler{} }

func (h *claimProducerObsHandler) Capabilities(_ context.Context, _ *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return &genv1.ClaimProducerObservabilityCapabilities{}, nil
}
