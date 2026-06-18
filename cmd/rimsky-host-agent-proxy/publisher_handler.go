// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (h *publisherHandler) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{Protocols: []string{protocolPublisher}}, nil
}

func (h *publisherHandler) Subscribe(ctx context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	var resp genv1.SubscribeResponse
	if rerr := h.forward(ctx, req.GetInstanceId(), "Subscribe", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

func (h *publisherHandler) Unsubscribe(ctx context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	var resp genv1.UnsubscribeResponse
	if rerr := h.forward(ctx, "", "Unsubscribe", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

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
