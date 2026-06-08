// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// validation_handler.go — the supervisor-facing Validation protocol
// handler. The proxy is a transparent forwarder: Validate resolves the
// dispatch to an owner's agent + lazily-spawned child (expected_protocols:
// [validation]), tunnels the serialized ValidateRequest via a DispatchFrame
// carrying rpc_method "Validate", awaits one response frame, and unmarshals
// it into ValidateResponse. ValidateRequest carries no instance_id, so the
// dispatch resolves purely by the x-rimsky-service-name header (the cached
// instance binding that names the late-bound validator). Proxy-side faults
// surface as gRPC error statuses carrying the error_class in a
// google.rpc.ErrorInfo detail — no protocol ships as a stub.
//
// @concept: host-agent-proxy

package main

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const protocolValidation = "validation"

// validationHandler implements genv1.ValidationServer.
type validationHandler struct {
	genv1.UnimplementedValidationServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
	callTimeout  time.Duration
}

func newValidationHandler(state *proxyState, cfg Config) *validationHandler {
	return &validationHandler{
		state:        state,
		fetch:        newControlAPIFetcher(&http.Client{Timeout: 10 * time.Second}, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
		callTimeout:  60 * time.Second,
	}
}

// Validate resolves+spawns the validator and forwards the unary Validate.
func (h *validationHandler) Validate(ctx context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	res, rerr := resolveAndSpawnByService(ctx, h.state, h.fetch, []string{protocolValidation}, "", "", h.spawnTimeout)
	if rerr != nil {
		return nil, proxyStatus(rerr)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, proxyStatus(&resolveError{class: errClassExecutorCrashed, msg: "marshal validate request: " + err.Error()})
	}
	respBytes, rerr := forwardProxyUnary(ctx, res.agent, res.spawnID, protocolValidation, "Validate", payload, h.callTimeout)
	if rerr != nil {
		return nil, proxyStatus(rerr)
	}
	var resp genv1.ValidateResponse
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		return nil, proxyStatus(&resolveError{class: errClassExecutorCrashed, msg: "unmarshal validate response: " + err.Error()})
	}
	return &resp, nil
}
