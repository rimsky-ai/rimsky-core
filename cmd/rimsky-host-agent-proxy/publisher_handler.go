// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// publisher_handler.go — the supervisor-facing Publisher protocol handler.
// The proxy is a transparent forwarder: each Publisher RPC resolves the
// dispatch to an owner's agent + lazily-spawned child (expected_protocols:
// [publisher]), tunnels the serialized request via a DispatchFrame carrying
// rpc_method, awaits one response frame, and unmarshals it into the
// protocol's response type. Proxy-side faults surface as gRPC error statuses
// carrying the error_class in a google.rpc.ErrorInfo detail, exactly as the
// executor and claim-producer handlers do — no protocol ships as a stub.
//
// @concept: host-agent-proxy

package main

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const protocolPublisher = "publisher"

// publisherHandler implements genv1.PublisherServer.
type publisherHandler struct {
	genv1.UnimplementedPublisherServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
	callTimeout  time.Duration
}

func newPublisherHandler(state *proxyState, cfg Config) *publisherHandler {
	return &publisherHandler{
		state:        state,
		fetch:        newControlAPIFetcher(&http.Client{Timeout: 10 * time.Second}, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
		callTimeout:  60 * time.Second,
	}
}

// Capabilities advertises a generic publisher envelope. The proxy is
// transport — the real capability surface comes from each spawned
// publisher's own Capabilities, read by the agent at handshake time.
func (h *publisherHandler) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{Protocols: []string{protocolPublisher}}, nil
}

// Subscribe resolves+spawns the publisher and forwards the unary Subscribe.
func (h *publisherHandler) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	var resp genv1.SubscribeResponse
	if rerr := h.forward(ctx, req.GetInstanceId(), "Subscribe", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

// Unsubscribe forwards the unary Unsubscribe to the spawned publisher.
// UnsubscribeRequest carries no instance_id, so resolution falls back to
// the x-rimsky-service-name header (service-name binding lookup).
func (h *publisherHandler) Unsubscribe(ctx context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	var resp genv1.UnsubscribeResponse
	if rerr := h.forward(ctx, "", "Unsubscribe", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

// forward resolves the dispatch, marshals req, tunnels it via the agent, and
// unmarshals the response frame into resp. instanceID is the request's
// instance_id when present; resolveAndSpawnByService falls back to
// service-name resolution when it is empty.
func (h *publisherHandler) forward(ctx context.Context, instanceID, rpcMethod string, req, resp proto.Message) *resolveError {
	res, rerr := resolveAndSpawnByService(ctx, h.state, h.fetch, []string{protocolPublisher}, instanceID, "", h.spawnTimeout)
	if rerr != nil {
		return rerr
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return &resolveError{class: errClassExecutorCrashed, msg: "marshal publisher request: " + err.Error()}
	}
	respBytes, rerr := forwardProxyUnary(ctx, res.agent, res.spawnID, protocolPublisher, rpcMethod, payload, h.callTimeout)
	if rerr != nil {
		return rerr
	}
	if err := proto.Unmarshal(respBytes, resp); err != nil {
		return &resolveError{class: errClassExecutorCrashed, msg: "unmarshal publisher response: " + err.Error()}
	}
	return nil
}
