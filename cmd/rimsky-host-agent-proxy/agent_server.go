// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent-proxy

package main

import (
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const proxyVersion = "v1"

type agentServer struct {
	genv1.UnimplementedHostAgentServer
	state    *proxyState
	forwards *httpForwarder
}

func newAgentServer(state *proxyState) *agentServer {
	return &agentServer{state: state, forwards: newHTTPForwarder(state)}
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
	apiKeyID := reg.GetApiKey()
	conn, prior, displaced := s.state.registerAgent(apiKeyID, reg.GetAgentLabel(), reg.GetLocalCallbackBaseUrl())
	if displaced && prior != nil {
		prior.close()
		prior.closeAllStreams()
		slog.Info("agent connection displaced prior", "api_key_id", redact(apiKeyID), "agent_label", reg.GetAgentLabel())
	}

	if !conn.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_RegisterAck{RegisterAck: &genv1.RegisterAck{
		ProxyVersion:   proxyVersion,
		DisplacedPrior: displaced,
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
	dropped := s.state.dropAgent(apiKeyID, conn)
	conn.closeAllStreams()
	<-writerDone
	if len(dropped) > 0 {
		slog.Info("agent disconnected; dropped spawns", "api_key_id", redact(apiKeyID), "spawn_count", len(dropped))
	}
	if errors.Is(readErr, io.EOF) {
		return nil
	}
	return readErr
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
			slog.Warn("ignoring duplicate Register on live stream", "api_key_id", redact(conn.apiKeyID))
		default:
			slog.Warn("unknown client frame body", "api_key_id", redact(conn.apiKeyID))
		}
	}
}

func redact(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}
