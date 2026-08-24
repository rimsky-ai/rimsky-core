// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func startDaemonFacingListener(t *testing.T, creds credentials.TransportCredentials) (addr string, state *proxyState) {
	t.Helper()
	state = newProxyState()
	var opts []grpc.ServerOption
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
	}
	srv := grpc.NewServer(opts...)
	genv1.RegisterHostDaemonServer(srv, newDaemonServer(state, presentedKeyIsIdentity))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), state
}

func registerOverHop(t *testing.T, addr string, creds credentials.TransportCredentials, apiKey string) error {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///"+enroll.ServiceServerName+":443",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return net.Dial("tcp", addr)
		}),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := genv1.NewHostDaemonClient(conn).Connect(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{
		Register: &genv1.Register{ApiKey: apiKey, LocalCallbackBaseUrl: "http://127.0.0.1:1"},
	}}); err != nil {
		return err
	}
	frame, err := stream.Recv()
	if err != nil {
		return err
	}
	if frame.GetRegisterAck() == nil {
		t.Fatalf("expected a RegisterAck, got %T", frame.GetBody())
	}
	return nil
}

// @decision: host-daemon-proxy-tls
// @concept: host-daemon
// @concept: host-daemon-proxy
func TestZeroConfigDaemonHopCarriesTheKeyInsideTLSUnderThePublishedRoot(t *testing.T) {
	served, err := proxyServerCredentials(Config{}, nil, time.Now)
	if err != nil {
		t.Fatalf("zero-config credentials: %v", err)
	}
	addr, state := startDaemonFacingListener(t, served.Credentials)

	pool, err := enroll.CAPoolFromPEM("published root", served.LocalCAPEM)
	if err != nil {
		t.Fatalf("parse the published root: %v", err)
	}
	const presentedKey = "rk_zero-config-over-tls"
	if err := registerOverHop(t, addr, credentials.NewTLS(enroll.PinnedTLSConfig(pool)), presentedKey); err != nil {
		t.Fatalf("register over the zero-config TLS hop: %v", err)
	}
	if _, ok := state.lookupDaemon(presentedKey); !ok {
		t.Fatal("the proxy did not register the daemon that connected over the zero-config TLS hop")
	}
}

// @decision: host-daemon-proxy-tls
func TestDaemonHopRefusesAPlaintextDialWhileTheProxyServesTLS(t *testing.T) {
	served, err := proxyServerCredentials(Config{}, nil, time.Now)
	if err != nil {
		t.Fatalf("zero-config credentials: %v", err)
	}
	addr, _ := startDaemonFacingListener(t, served.Credentials)

	if err := registerOverHop(t, addr, insecure.NewCredentials(), "rk_leaks-if-accepted"); err == nil {
		t.Fatal("a plaintext dial must fail against a proxy serving TLS; the switch is set on both ends or neither")
	}
}

// @decision: host-daemon-proxy-tls
func TestDaemonHopRunsPlaintextWhenBothEndsCarryTheInsecureSwitch(t *testing.T) {
	served, err := proxyServerCredentials(Config{Insecure: true}, nil, time.Now)
	if err != nil {
		t.Fatalf("insecure credentials: %v", err)
	}
	addr, state := startDaemonFacingListener(t, served.Credentials)

	const presentedKey = "rk_plaintext-by-agreement"
	if err := registerOverHop(t, addr, insecure.NewCredentials(), presentedKey); err != nil {
		t.Fatalf("register over the agreed plaintext hop: %v", err)
	}
	if _, ok := state.lookupDaemon(presentedKey); !ok {
		t.Fatal("the proxy did not register the daemon that connected over the agreed plaintext hop")
	}
}
