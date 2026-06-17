// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// executor_handler.go — the supervisor-facing Executor protocol handler.
// Per TD-execute-rpc-unary the Executor.Execute RPC is unary: the
// proxy resolves the dispatch to an owner's agent + spawned child,
// rewrites the callback URL onto the agent's local listener, tunnels
// the ExecuteRequest into the child via a DispatchFrame, awaits the
// agent's reply DispatchFrame carrying the Outcome, unmarshals it,
// and returns it on the unary call. Proxy-side failures surface as
// Outcome{Error{error_class}}.
//
// @concept: host-agent-proxy

package main

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const protocolExecutor = "executor"

// executorHandler implements genv1.ExecutorServer.
type executorHandler struct {
	genv1.UnimplementedExecutorServer
	state        *proxyState
	fetch        instanceFetcher
	spawnTimeout time.Duration
}

func newExecutorHandler(state *proxyState, cfg Config) *executorHandler {
	return &executorHandler{
		state:        state,
		fetch:        newControlAPIFetcher(&http.Client{Timeout: 10 * time.Second}, cfg.ControlAPIURL, cfg.ControlAPIToken),
		spawnTimeout: cfg.SpawnReadyTimeout,
	}
}

// Execute resolves, spawns, tunnels, and returns the unary Outcome.
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

	// @constraint: rewrite the callback URL onto the agent's local
	// listener so the spawned child posts callbacks into the tunnel
	// rather than dialing the supervisor directly (which it can't reach).
	forwarded := proto.Clone(req).(*genv1.ExecuteRequest)
	forwarded.CallbackUrl = rewriteCallbackURL(req.GetCallbackUrl(), res.agent.localCallbackBaseURL)

	payload, err := proto.Marshal(forwarded)
	if err != nil {
		return executorTerminalError(errClassExecutorCrashed, "marshal execute request: "+err.Error()), nil
	}

	streamID := uuid.NewString()
	respCh := res.agent.registerStream(streamID)
	defer res.agent.clearStream(streamID)

	if !res.agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  res.spawnID,
		Protocol: protocolExecutor,
		Payload:  payload,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}}) {
		return executorTerminalError(errClassHostAgentDisconnected, "agent disconnected before dispatch"), nil
	}

	select {
	case <-ctx.Done():
		// @constraint: the supervisor cancelled the Execute call; send
		// a terminal CANCEL frame so the spawned executor's call is
		// torn down rather than left running until the child terminates
		// on its own.
		res.agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
			SpawnId:  res.spawnID,
			Protocol: protocolExecutor,
			StreamId: streamID,
			Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
		}}})
		return nil, ctx.Err()
	case frame, ok := <-respCh:
		if !ok {
			h.state.dropSpawn(res.spawnID)
			return executorTerminalError(errClassHostAgentDisconnected, "agent disconnected mid-execute"), nil
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

// executorTerminalError synthesises an Outcome{Error{class}} so a
// proxy-side failure surfaces through the same terminal-handler
// pipeline a real executor's Error outcome takes.
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

// executorObsHandler implements genv1.ExecutorObservabilityServer. The
// proxy itself has no fixed capability schema — it serves whatever name
// the supervisor dispatches — so it advertises an empty envelope.
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
