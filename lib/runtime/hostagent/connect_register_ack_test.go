// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type silentProxy struct {
	genv1.UnimplementedHostAgentServer
	received chan struct{}
}

func (s *silentProxy) Connect(stream genv1.HostAgent_ConnectServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	close(s.received)
	<-stream.Context().Done()
	return stream.Context().Err()
}

func startSilentProxy(t *testing.T) (addr string, received <-chan struct{}) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	sp := &silentProxy{received: make(chan struct{})}
	srv := grpc.NewServer()
	genv1.RegisterHostAgentServer(srv, sp)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), sp.received
}

func TestConnectOnce_RegisterAckTimeoutBounds(t *testing.T) {
	addr, received := startSilentProxy(t)
	cfg := Config{
		ProxyURL:           addr,
		APIKey:             "k",
		RegisterAckTimeout: 50 * time.Millisecond,
	}.withDefaults()

	trust, err := newLocalTrust(time.Now())
	if err != nil {
		t.Fatalf("newLocalTrust: %v", err)
	}

	_, err = connectOnce(context.Background(), cfg, trust, "http://127.0.0.1:0", "https://127.0.0.1:0")
	if err == nil {
		t.Fatal("expected connectOnce to fail when the proxy never sends a RegisterAck, got nil error")
	}
	if !strings.Contains(err.Error(), "RegisterAck") {
		t.Fatalf("error = %v, want it to name RegisterAck", err)
	}
	<-received
}

func TestRecvWithTimeout_ReturnsFrameWhenDeliveredBeforeDeadline(t *testing.T) {
	fp := startFakeProxy(t)
	trust, err := newLocalTrust(time.Now())
	if err != nil {
		t.Fatalf("newLocalTrust: %v", err)
	}
	cfg := Config{ProxyURL: fp.addr, APIKey: "k", RegisterAckTimeout: 5 * time.Second}.withDefaults()
	a, err := connectOnce(context.Background(), cfg, trust, "http://127.0.0.1:0", "https://127.0.0.1:0")
	if err != nil {
		t.Fatalf("connectOnce: %v", err)
	}
	if a.proxyConn == nil {
		t.Fatal("expected a non-nil proxy connection after a successful RegisterAck")
	}
	_ = a.proxyConn.Close()
}
