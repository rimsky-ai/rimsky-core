// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy

package main

import (
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const proxyVersion = "v1"

const anonymousSillyNameCollisionRetries = 32

type agentServer struct {
	genv1.UnimplementedHostAgentServer
	state          *proxyState
	forwards       *httpForwarder
	verifyIdentity registerIdentityVerifier
	generateName   func() string
}

func newAgentServer(state *proxyState, verifyIdentity registerIdentityVerifier) *agentServer {
	return &agentServer{
		state:          state,
		forwards:       newHTTPForwarder(state),
		verifyIdentity: verifyIdentity,
		generateName:   sillyname.Generate,
	}
}

func (s *agentServer) Connect(stream genv1.HostAgent_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "first frame must be Register")
	}
	if reg.GetApiKey() == "" {
		return status.Error(codes.InvalidArgument, "Register.api_key is required")
	}

	// @concept: host-agent-proxy
	// @concept: api-key
	verdict, err := s.verifyIdentity(stream.Context(), reg.GetApiKey())
	if err != nil {
		slog.Warn("agent register rejected", "agent_label", reg.GetAgentLabel(), "error", err)
		return err
	}

	conn, routingIdentity, prior, err := s.adoptRoutingIdentity(verdict, reg)
	if err != nil {
		slog.Warn("agent register rejected", "agent_label", reg.GetAgentLabel(), "routing_label", reg.GetRoutingLabel(), "error", err)
		return err
	}
	displaced := prior != nil
	if displaced {
		prior.close()
		prior.closeAllStreams()
		slog.Info("agent connection displaced prior", "routing_identity", redact(routingIdentity), "agent_label", reg.GetAgentLabel())
	}

	if !conn.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_RegisterAck{RegisterAck: &genv1.RegisterAck{
		ProxyVersion:    proxyVersion,
		DisplacedPrior:  displaced,
		RoutingIdentity: routingIdentity,
	}}}) {
		conn.close()
		return status.Error(codes.Unavailable, "connection closed before register ack")
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case frame := <-conn.sendCh:
				if frame == nil {
					return
				}
				if err := stream.Send(frame); err != nil {
					conn.close()
					return
				}
			case <-conn.closed:
				return
			}
		}
	}()

	readErr := s.readLoop(stream, conn)

	conn.close()
	dropped := s.state.dropAgent(routingIdentity, conn)
	conn.closeAllStreams()
	<-writerDone
	if len(dropped) > 0 {
		slog.Info("agent disconnected; dropped spawns", "routing_identity", redact(routingIdentity), "spawn_count", len(dropped))
	}
	if errors.Is(readErr, io.EOF) {
		return nil
	}
	return readErr
}

// @concept: host-agent-proxy
// @concept: anonymous-mode
func (s *agentServer) adoptRoutingIdentity(verdict registerIdentityVerdict, reg *genv1.Register) (*agentConnection, string, *agentConnection, error) {
	label := reg.GetAgentLabel()
	callback := reg.GetLocalCallbackBaseUrl()
	if verdict.kind == registerIdentityAPIKey {
		conn, prior, _ := s.state.registerAgent(verdict.keyID, label, callback, registerDisplacePrior)
		return conn, verdict.keyID, prior, nil
	}
	presentedLabel := reg.GetRoutingLabel()
	if presentedLabel != "" {
		if err := sillyname.Validate(presentedLabel); err != nil {
			return nil, "", nil, status.Errorf(codes.InvalidArgument, "Register.routing_label rejected: %v", err)
		}
		conn, _, collided := s.state.registerAgent(presentedLabel, label, callback, registerRejectOnCollision)
		if collided {
			return nil, "", nil, status.Errorf(codes.AlreadyExists,
				"Register.routing_label %q is already in use by another currently-connected anonymous agent; pick a different label or omit it to have one assigned", presentedLabel)
		}
		return conn, presentedLabel, nil, nil
	}
	for i := 0; i < anonymousSillyNameCollisionRetries; i++ {
		candidate := s.generateName()
		conn, _, collided := s.state.registerAgent(candidate, label, callback, registerRejectOnCollision)
		if !collided {
			return conn, candidate, nil, nil
		}
	}
	return nil, "", nil, status.Errorf(codes.ResourceExhausted, "unable to assign a fresh anonymous silly-name after %d attempts; the proxy is saturated with connected anonymous agents", anonymousSillyNameCollisionRetries)
}

func (s *agentServer) readLoop(stream genv1.HostAgent_ConnectServer, conn *agentConnection) error {
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		switch body := frame.GetBody().(type) {
		case *genv1.ClientFrame_Heartbeat:
			conn.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_HeartbeatAck{HeartbeatAck: &genv1.HostAgentHeartbeatAck{
				ReceivedAtUnixMs: nowUnixMs(),
			}}})
		case *genv1.ClientFrame_SpawnAck:
			conn.deliverSpawnAck(body.SpawnAck)
		case *genv1.ClientFrame_Reaped:
			conn.deliverReaped(body.Reaped)
		case *genv1.ClientFrame_DispatchFrame:
			conn.deliverDispatch(body.DispatchFrame)
		case *genv1.ClientFrame_HttpForward:
			go s.forwards.handle(conn, body.HttpForward)
		case *genv1.ClientFrame_Register:
			slog.Warn("ignoring duplicate Register on live stream", "routing_identity", redact(conn.routingIdentity))
		default:
			slog.Warn("unknown client frame body", "routing_identity", redact(conn.routingIdentity))
		}
	}
}

func redact(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}
