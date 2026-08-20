// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func newAgentTestServer(t *testing.T) (*proxyState, genv1.HostAgentClient) {
	t.Helper()
	return newAgentTestServerWithVerifier(t, presentedKeyIsIdentity)
}

func newAgentTestServerWithVerifier(t *testing.T, verify registerIdentityVerifier) (*proxyState, genv1.HostAgentClient) {
	t.Helper()
	state := newProxyState()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	genv1.RegisterHostAgentServer(srv, newAgentServer(state, verify))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return state, genv1.NewHostAgentClient(conn)
}

func TestConnectRequiresRegisterFirst(t *testing.T) {
	_, client := newAgentTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{Heartbeat: &genv1.HostAgentHeartbeat{}}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestRegisterAck(t *testing.T) {
	state, client := newAgentTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	mustSend(t, stream, &genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               "key-1",
		AgentLabel:           "host-a",
		LocalCallbackBaseUrl: "http://127.0.0.1:5001",
	}}})
	frame, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv ack: %v", err)
	}
	ack := frame.GetRegisterAck()
	if ack == nil {
		t.Fatalf("expected RegisterAck, got %T", frame.GetBody())
	}
	if ack.GetDisplacedPrior() {
		t.Fatalf("first register should not displace")
	}
	if _, ok := state.lookupAgent("key-1"); !ok {
		t.Fatalf("agent should be registered in state")
	}
}

func TestHeartbeatRoundTrip(t *testing.T) {
	_, client := newAgentTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, _ := client.Connect(ctx)
	registerAndAck(t, stream, "key-1", "")

	mustSend(t, stream, &genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{Heartbeat: &genv1.HostAgentHeartbeat{SentAtUnixMs: 123}}})
	frame, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv heartbeat ack: %v", err)
	}
	if frame.GetHeartbeatAck() == nil {
		t.Fatalf("expected HeartbeatAck, got %T", frame.GetBody())
	}
}

func TestDuplicateRegisterDisplacesPrior(t *testing.T) {
	state, client := newAgentTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s1, _ := client.Connect(ctx)
	registerAndAck(t, s1, "key-1", "")

	s2, _ := client.Connect(ctx)
	mustSend(t, s2, &genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{ApiKey: "key-1", AgentLabel: "host-b"}}})
	frame, err := s2.Recv()
	if err != nil {
		t.Fatalf("recv ack on second: %v", err)
	}
	if !frame.GetRegisterAck().GetDisplacedPrior() {
		t.Fatalf("second register should report displaced_prior")
	}
	if got, ok := state.lookupAgent("key-1"); !ok || got.agentLabel != "host-b" {
		t.Fatalf("state should hold the second connection")
	}
}

func TestSpawnAckCorrelation(t *testing.T) {
	state, client := newAgentTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, _ := client.Connect(ctx)
	registerAndAck(t, stream, "key-1", "")

	conn, ok := state.lookupAgent("key-1")
	if !ok {
		t.Fatalf("agent not registered")
	}

	ackCh := conn.registerSpawnPending("spawn-1")

	mustSend(t, stream, &genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: &genv1.SpawnAck{
		SpawnId: "spawn-1",
		Status:  genv1.SpawnAck_SPAWN_STATUS_READY,
	}}})

	if ack := <-ackCh; ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("unexpected status: %v", ack.GetStatus())
	}
}

func TestStreamCloseDropsAgentAndNotifies(t *testing.T) {
	state, client := newAgentTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, _ := client.Connect(ctx)
	registerAndAck(t, stream, "key-1", "")
	conn, _ := state.lookupAgent("key-1")

	respCh := conn.registerStream("stream-1")

	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}

	if _, open := <-respCh; open {
		t.Fatalf("expected dispatch channel to be closed on disconnect")
	}

	waitFor(t, "the proxy to drop the agent after its stream closed",
		func() bool { _, ok := state.lookupAgent("key-1"); return !ok })
}

func mustSend(t *testing.T, stream genv1.HostAgent_ConnectClient, frame *genv1.ClientFrame) {
	t.Helper()
	if err := stream.Send(frame); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func registerAndAck(t *testing.T, stream genv1.HostAgent_ConnectClient, apiKey, localBase string) {
	t.Helper()
	mustSend(t, stream, &genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               apiKey,
		LocalCallbackBaseUrl: localBase,
	}}})
	frame, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register ack: %v", err)
	}
	if frame.GetRegisterAck() == nil {
		t.Fatalf("expected RegisterAck, got %T", frame.GetBody())
	}
}
