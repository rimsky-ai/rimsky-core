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

const protocolDataProcessing = "data_processing"

type dataProcessingHandler struct {
	genv1.UnimplementedDataProcessingServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
	callTimeout  time.Duration
}

func newDataProcessingHandler(state *proxyState, cfg Config) *dataProcessingHandler {
	return &dataProcessingHandler{
		state:        state,
		fetch:        newControlAPIFetcher(&http.Client{Timeout: 10 * time.Second}, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
		callTimeout:  60 * time.Second,
	}
}

func (h *dataProcessingHandler) Capabilities(_ context.Context, _ *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{Materializations: []string{"full"}}, nil
}

func (h *dataProcessingHandler) BeginCandidate(ctx context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	var resp genv1.BeginCandidateResponse
	if rerr := h.forward(ctx, "BeginCandidate", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

func (h *dataProcessingHandler) CommitCandidate(ctx context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	var resp genv1.CommitCandidateResponse
	if rerr := h.forward(ctx, "CommitCandidate", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

func (h *dataProcessingHandler) AbandonCandidate(ctx context.Context, req *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	var resp emptypb.Empty
	if rerr := h.forward(ctx, "AbandonCandidate", req, &resp); rerr != nil {
		return nil, proxyStatus(rerr)
	}
	return &resp, nil
}

func (h *dataProcessingHandler) forward(ctx context.Context, rpcMethod string, req, resp proto.Message) *resolveError {
	res, rerr := resolveAndSpawnByService(ctx, h.state, h.fetch, []string{protocolDataProcessing}, "", "", h.spawnTimeout)
	if rerr != nil {
		return rerr
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return &resolveError{class: errClassExecutorCrashed, msg: "marshal data-processing request: " + err.Error()}
	}
	respBytes, rerr := forwardProxyUnary(ctx, res.agent, res.spawnID, protocolDataProcessing, rpcMethod, payload, h.callTimeout)
	if rerr != nil {
		return rerr
	}
	if err := proto.Unmarshal(respBytes, resp); err != nil {
		return &resolveError{class: errClassExecutorCrashed, msg: "unmarshal data-processing response: " + err.Error()}
	}
	return nil
}
