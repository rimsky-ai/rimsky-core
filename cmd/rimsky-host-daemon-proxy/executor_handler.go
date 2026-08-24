// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const protocolExecutor = "executor"

type executorHandler struct {
	genv1.UnimplementedExecutorServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
}

func newExecutorHandler(state *proxyState, cfg Config, controlAPIClient *http.Client) *executorHandler {
	return &executorHandler{
		state:        state,
		fetch:        newControlAPIFetcher(controlAPIClient, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
	}
}

func (h *executorHandler) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	res, rerr := resolveAndSpawn(
		ctx, h.state, h.fetch,
		[]string{protocolExecutor},
		req.GetInstanceId(),
		req.GetRunScopeId(),
		req.GetCallbackUrl(),
		h.spawnTimeout,
	)
	if rerr != nil {
		return executorTerminalError(rerr.class, rerr.msg), nil
	}

	if rerr := h.checkContract(res, req); rerr != nil {
		return executorTerminalError(rerr.class, rerr.msg), nil
	}

	forwarded := proto.Clone(req).(*genv1.ExecuteRequest)
	forwarded.CallbackUrl = rewriteCallbackURL(req.GetCallbackUrl(), res.daemon.localCallbackBaseURL)

	payload, err := proto.Marshal(forwarded)
	if err != nil {
		return executorTerminalError(errClassExecutorCrashed, "marshal execute request: "+err.Error()), nil
	}

	streamID := uuid.NewString()
	respCh := res.daemon.registerStream(streamID)
	defer res.daemon.clearStream(streamID)

	if !sendDispatchFrame(res.daemon, res.spawnID, protocolExecutor, streamID, payload, nil) {
		return executorTerminalError(errClassHostDaemonDisconnected, "daemon disconnected before dispatch"), nil
	}

	select {
	case <-ctx.Done():
		res.daemon.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
			SpawnId:  res.spawnID,
			Protocol: protocolExecutor,
			StreamId: streamID,
			Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
		}}})
		return nil, ctx.Err()
	case frame, ok := <-respCh:
		if !ok {
			h.state.dropSpawn(res.spawnID)
			return executorTerminalError(errClassHostDaemonDisconnected, "daemon disconnected mid-execute"), nil
		}
		if frame.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
			h.state.dropSpawn(res.spawnID)
			return executorTerminalError(errClassExecutorCrashed, "spawned executor cancelled the call"), nil
		}
		var outcome genv1.Outcome
		if err := proto.Unmarshal(frame.GetPayload(), &outcome); err != nil {
			h.state.dropSpawn(res.spawnID)
			return executorTerminalError(errClassExecutorCrashed, "unmarshal outcome: "+err.Error()), nil
		}
		return &outcome, nil
	}
}

func (h *executorHandler) checkContract(res *resolved, req *genv1.ExecuteRequest) *resolveError {
	sp, ok := h.state.lookupSpawn(res.spawnID)
	if !ok {
		return nil
	}
	raw, ok := sp.capabilities[protocolExecutor]
	if !ok || len(raw) == 0 {
		return nil
	}
	var caps genv1.ObservabilityCapabilities
	if err := proto.Unmarshal(raw, &caps); err != nil {
		return nil
	}
	schemaBytes := caps.GetExpectedAttributesSchema()
	if len(schemaBytes) == 0 {
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return &resolveError{
			class: errClassContractMismatch,
			msg:   "spawned binary's expected_attributes_schema is not valid JSON: " + err.Error(),
		}
	}
	if err := attributes.Validate(schema, req.GetAttributes().AsMap(), attributes.PhaseDispatch); err != nil {
		return &resolveError{
			class: errClassContractMismatch,
			msg:   "resolved attributes do not satisfy the spawned binary's declared schema: " + err.Error(),
		}
	}
	return nil
}

func executorTerminalError(class, message string) *genv1.Outcome {
	var payload *structpb.Struct
	if message != "" {
		payload, _ = structpb.NewStruct(map[string]any{"message": message})
	}
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Error{
			Error: &genv1.Error{
				ErrorClass: class,
				Payload:    payload,
			},
		},
	}
}

type executorObsHandler struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func newExecutorObsHandler() *executorObsHandler { return &executorObsHandler{} }

func (h *executorObsHandler) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		DeclaredTags:             nil,
		ExpectedAttributesSchema: nil,
	}, nil
}
