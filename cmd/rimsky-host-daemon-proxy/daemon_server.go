// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy

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

type daemonServer struct {
	genv1.UnimplementedHostDaemonServer
	state          *proxyState
	forwards       *httpForwarder
	verifyIdentity registerIdentityVerifier
	generateName   func() string
}

func newDaemonServer(state *proxyState, verifyIdentity registerIdentityVerifier) *daemonServer {
	return &daemonServer{
		state:          state,
		forwards:       newHTTPForwarder(state),
		verifyIdentity: verifyIdentity,
		generateName:   sillyname.Generate,
	}
}

func (s *daemonServer) Connect(stream genv1.HostDaemon_ConnectServer) error {
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

	// @concept: host-daemon-proxy
	// @concept: api-key
	verdict, err := s.verifyIdentity(stream.Context(), reg.GetApiKey())
	if err != nil {
		slog.Warn("PROXY.DAEMONREGISTER.REJECTED", "daemon_label", reg.GetDaemonLabel(), "error", err)
		return err
	}

	conn, routingIdentity, prior, err := s.adoptRoutingIdentity(verdict, reg)
	if err != nil {
		slog.Warn("PROXY.DAEMONREGISTER.REJECTED", "daemon_label", reg.GetDaemonLabel(), "routing_label", reg.GetRoutingLabel(), "error", err)
		return err
	}
	displaced := prior != nil
	if displaced {
		prior.close()
		prior.closeAllStreams()
		slog.Info("PROXY.DAEMONCONNECTION.DISPLACED", "routing_identity", redact(routingIdentity), "daemon_label", reg.GetDaemonLabel())
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
	dropped := s.state.dropDaemon(routingIdentity, conn)
	conn.closeAllStreams()
	<-writerDone
	if len(dropped) > 0 {
		slog.Info("PROXY.SPAWNS.DROPPED", "detail", "the daemon disconnected", "routing_identity", redact(routingIdentity), "spawn_count", len(dropped))
	}
	if errors.Is(readErr, io.EOF) {
		return nil
	}
	return readErr
}

// @concept: host-daemon-proxy
// @concept: anonymous-mode
func (s *daemonServer) adoptRoutingIdentity(verdict registerIdentityVerdict, reg *genv1.Register) (*daemonConnection, string, *daemonConnection, error) {
	label := reg.GetDaemonLabel()
	callback := reg.GetLocalCallbackBaseUrl()
	if verdict.kind == registerIdentityAPIKey {
		conn, prior, _ := s.state.registerDaemon(verdict.keyID, label, callback, registerDisplacePrior)
		return conn, verdict.keyID, prior, nil
	}
	presentedLabel := reg.GetRoutingLabel()
	if presentedLabel != "" {
		if err := sillyname.Validate(presentedLabel); err != nil {
			return nil, "", nil, status.Errorf(codes.InvalidArgument, "Register.routing_label rejected: %v", err)
		}
		conn, _, collided := s.state.registerDaemon(presentedLabel, label, callback, registerRejectOnCollision)
		if collided {
			return nil, "", nil, status.Errorf(codes.AlreadyExists,
				"Register.routing_label %q is already in use by another currently-connected anonymous daemon; pick a different label or omit it to have one assigned", presentedLabel)
		}
		return conn, presentedLabel, nil, nil
	}
	for i := 0; i < anonymousSillyNameCollisionRetries; i++ {
		candidate := s.generateName()
		conn, _, collided := s.state.registerDaemon(candidate, label, callback, registerRejectOnCollision)
		if !collided {
			return conn, candidate, nil, nil
		}
	}
	return nil, "", nil, status.Errorf(codes.ResourceExhausted, "unable to assign a fresh anonymous silly-name after %d attempts; the proxy is saturated with connected anonymous daemons", anonymousSillyNameCollisionRetries)
}

func (s *daemonServer) readLoop(stream genv1.HostDaemon_ConnectServer, conn *daemonConnection) error {
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		switch body := frame.GetBody().(type) {
		case *genv1.ClientFrame_Heartbeat:
			conn.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_HeartbeatAck{HeartbeatAck: &genv1.HostDaemonHeartbeatAck{
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
			slog.Warn("PROXY.DUPLICATEREGISTER.IGNORED", "routing_identity", redact(conn.routingIdentity))
		default:
			slog.Warn("PROXY.CLIENTFRAME.UNKNOWN", "routing_identity", redact(conn.routingIdentity))
		}
	}
}

func redact(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}
