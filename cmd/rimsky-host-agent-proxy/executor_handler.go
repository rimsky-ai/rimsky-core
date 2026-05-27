// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// executor_handler.go — the supervisor-facing Executor protocol handler.
// Implements the spec's "Proxy on a supervisor's Executor.Execute(req)
// call": resolve the dispatch to an owner's agent + spawned child,
// rewrite the callback URL onto the agent's local listener, tunnel the
// ExecuteRequest into the child via a DispatchFrame, stream the child's
// ExecuteEvents back to the supervisor, and translate disconnects into a
// synthetic terminal StreamClose. Proxy-side failures surface as
// StreamClose{Error, error_class} on the streaming RPC.
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

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
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

// Execute resolves, spawns, tunnels, and streams an executor dispatch.
func (h *executorHandler) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	ctx := stream.Context()

	res, rerr := resolveAndSpawn(
		ctx, h.state, h.fetch,
		[]string{protocolExecutor},
		req.GetInstanceId(),
		req.GetCallbackUrl(),
		h.spawnTimeout,
	)
	if rerr != nil {
		return sendExecutorTerminalError(stream, rerr.class, rerr.msg)
	}

	// Rewrite the callback URL onto the agent's local listener so the
	// spawned child posts callbacks into the tunnel rather than dialing
	// the supervisor directly (which it can't reach).
	forwarded := proto.Clone(req).(*genv1.ExecuteRequest)
	forwarded.CallbackUrl = rewriteCallbackURL(req.GetCallbackUrl(), res.agent.localCallbackBaseURL)

	payload, err := proto.Marshal(forwarded)
	if err != nil {
		return sendExecutorTerminalError(stream, errClassExecutorCrashed, "marshal execute request: "+err.Error())
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
		return sendExecutorTerminalError(stream, errClassHostAgentDisconnected, "agent disconnected before dispatch")
	}

	return h.pump(stream, res, streamID, respCh)
}

// pump reads response DispatchFrames, forwards inner ExecuteEvents to the
// supervisor, and returns when the inner stream terminates or the agent
// disconnects.
func (h *executorHandler) pump(stream genv1.Executor_ExecuteServer, res *resolved, streamID string, respCh chan *genv1.DispatchFrame) error {
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			// The supervisor cancelled the Execute stream. Signal the agent
			// to cancel the child's inner Execute via a terminal CANCEL frame
			// so the spawned executor's stream is torn down rather than left
			// running until the child terminates on its own.
			res.agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
				SpawnId:  res.spawnID,
				Protocol: protocolExecutor,
				StreamId: streamID,
				Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
			}}})
			return ctx.Err()
		case frame, ok := <-respCh:
			if !ok {
				// Channel closed by closeAllStreams on agent disconnect.
				h.state.dropSpawn(res.spawnID)
				return sendExecutorTerminalError(stream, errClassHostAgentDisconnected, "agent disconnected mid-execute")
			}
			if frame.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
				h.state.dropSpawn(res.spawnID)
				return sendExecutorTerminalError(stream, errClassExecutorCrashed, "spawned executor cancelled the stream")
			}
			var ev genv1.ExecuteEvent
			if err := proto.Unmarshal(frame.GetPayload(), &ev); err != nil {
				h.state.dropSpawn(res.spawnID)
				return sendExecutorTerminalError(stream, errClassExecutorCrashed, "unmarshal execute event: "+err.Error())
			}
			if err := stream.Send(&ev); err != nil {
				return err
			}
			if _, terminal := ev.GetEvent().(*genv1.ExecuteEvent_StreamClose); terminal {
				return nil
			}
		}
	}
}

// sendExecutorTerminalError emits a single terminal StreamClose{Error}
// with the given error_class on the supervisor-facing Execute stream.
// The human-readable message rides Error.payload.message for diagnostics.
func sendExecutorTerminalError(stream genv1.Executor_ExecuteServer, class, message string) error {
	var payload *structpb.Struct
	if message != "" {
		payload, _ = structpb.NewStruct(map[string]any{"message": message})
	}
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
			ErrorClass: class,
			Payload:    payload,
		}}},
	}})
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
		DeclaredEvents:           nil,
		ExpectedAttributesSchema: nil,
	}, nil
}
