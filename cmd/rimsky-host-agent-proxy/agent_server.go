// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// agent_server.go — the agent-facing HostAgent.Connect handler. One
// long-lived bidi stream per connected dev-machine agent. The first
// frame must be Register; thereafter a reader goroutine routes inbound
// ClientFrames by oneof and a writer goroutine drains the connection's
// sendCh to the stream. On disconnect the connection is dropped and any
// in-flight dispatch readers are notified via closed stream channels.
//
// @concept: host-agent-proxy

package main

import (
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// proxyVersion is reported to agents on RegisterAck.
const proxyVersion = "v1"

// agentServer implements genv1.HostAgentServer.
type agentServer struct {
	genv1.UnimplementedHostAgentServer
	state    *proxyState
	forwards *httpForwarder
}

func newAgentServer(state *proxyState) *agentServer {
	return &agentServer{state: state, forwards: newHTTPForwarder(state)}
}

// Connect handles one agent's bidi stream for its whole lifetime.
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

	// The api-key id keys the agent index. v1 uses the api-key string
	// verbatim as the routing key; control-api supplies the owner-api-key
	// id on the same routing key via OnInstanceCreated.
	apiKeyID := reg.GetApiKey()
	conn, prior, displaced := s.state.registerAgent(apiKeyID, reg.GetAgentLabel(), reg.GetLocalCallbackBaseUrl())
	if displaced && prior != nil {
		// Gracefully close the displaced prior connection's writer.
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

	// Writer goroutine: drain sendCh to the stream until closed.
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

	// Reader loop: route inbound frames until the stream errors / EOFs.
	readErr := s.readLoop(stream, conn)

	// Teardown: drop the agent, close the writer, and notify in-flight
	// dispatch readers via closed stream channels.
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

// readLoop routes each inbound ClientFrame to its handler/channel.
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
			// Handle the local-HTTP-forward out of band so it doesn't
			// block the reader loop on the upstream POST.
			go s.forwards.handle(conn, body.HttpForward)
		case *genv1.ClientFrame_Register:
			// A second Register on an already-registered stream is a
			// protocol error; ignore it (the agent should open a fresh
			// stream to re-register).
			slog.Warn("ignoring duplicate Register on live stream", "api_key_id", redact(conn.apiKeyID))
		default:
			slog.Warn("unknown client frame body", "api_key_id", redact(conn.apiKeyID))
		}
	}
}

// redact trims an api-key to a short prefix for logging.
func redact(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}
