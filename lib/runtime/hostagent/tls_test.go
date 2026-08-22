// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const tlsProxyServerName = "proxy-host"

type capturingProxy struct {
	genv1.UnimplementedHostAgentServer
	mu           sync.Mutex
	gotAPIKey    string
	registered   chan struct{}
	registerOnce sync.Once
}

func (p *capturingProxy) Connect(stream genv1.HostAgent_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return context.Canceled
	}
	p.mu.Lock()
	p.gotAPIKey = reg.GetApiKey()
	p.mu.Unlock()
	p.registerOnce.Do(func() { close(p.registered) })
	return stream.Send(&genv1.ServerFrame{Body: &genv1.ServerFrame_RegisterAck{RegisterAck: &genv1.RegisterAck{ProxyVersion: "tls-test"}}})
}

func startTLSProxy(t *testing.T, ca *pki.CA) (addr string, proxy *capturingProxy) {
	t.Helper()
	issued, err := ca.IssueLeaf(tlsProxyServerName, time.Now().Add(-time.Hour), 2*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxy = &capturingProxy{registered: make(chan struct{})}
	srv := grpc.NewServer(grpc.Creds(creds))
	genv1.RegisterHostAgentServer(srv, proxy)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), proxy
}

func writeCAFile(t *testing.T, ca *pki.CA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, ca.CertPEM(), 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}

func dialProxy(realAddr string, creds credentials.TransportCredentials) (*grpc.ClientConn, error) {
	return grpc.NewClient("passthrough:///"+tlsProxyServerName+":443",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return net.Dial("tcp", realAddr)
		}),
		grpc.WithTransportCredentials(creds),
	)
}

func TestAgentTLSDialTrustsPinnedCAAndCarriesKey(t *testing.T) {
	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	addr, proxy := startTLSProxy(t, ca)

	cfg := Config{TLSCAPath: writeCAFile(t, ca)}
	creds, err := agentTransportCredentials(cfg)
	if err != nil {
		t.Fatalf("agentTransportCredentials: %v", err)
	}
	conn, err := dialProxy(addr, creds)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := genv1.NewHostAgentClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	const presentedKey = "rk_secret-over-tls"
	if err := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{ApiKey: presentedKey}}}); err != nil {
		t.Fatalf("send register: %v", err)
	}
	frame, err := stream.Recv()
	if err != nil || frame.GetRegisterAck() == nil {
		t.Fatalf("expected RegisterAck over TLS: err=%v frame=%T", err, frame.GetBody())
	}
	<-proxy.registered
	proxy.mu.Lock()
	got := proxy.gotAPIKey
	proxy.mu.Unlock()
	if got != presentedKey {
		t.Fatalf("proxy received api-key %q over TLS, want %q", got, presentedKey)
	}
}

func TestAgentTLSDialRejectsWrongCAPin(t *testing.T) {
	serverCA, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA server: %v", err)
	}
	impostorCA, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	addr, _ := startTLSProxy(t, serverCA)

	cfg := Config{TLSCAPath: writeCAFile(t, impostorCA)}
	creds, err := agentTransportCredentials(cfg)
	if err != nil {
		t.Fatalf("agentTransportCredentials: %v", err)
	}
	conn, err := dialProxy(addr, creds)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := genv1.NewHostAgentClient(conn).Connect(ctx)
	if err != nil {
		return
	}
	if sendErr := stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{ApiKey: "rk_leaks-if-accepted"}}}); sendErr != nil {
		return
	}
	if _, recvErr := stream.Recv(); recvErr == nil {
		t.Fatal("handshake against a wrong CA pin must fail before the key is usable, got a RegisterAck")
	}
}

// @decision: host-agent-proxy-tls
func TestAgentTransportCredentialsDialTLSByDefault(t *testing.T) {
	creds, err := agentTransportCredentials(Config{})
	if err != nil {
		t.Fatalf("default creds: %v", err)
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Fatalf("the dial to the proxy must default to TLS, got %q", creds.Info().SecurityProtocol)
	}
}

// @decision: host-agent-proxy-tls
func TestAgentTransportCredentialsPlaintextOnlyBehindTheInsecureSwitch(t *testing.T) {
	creds, err := agentTransportCredentials(Config{Insecure: true})
	if err != nil {
		t.Fatalf("insecure creds: %v", err)
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("the insecure switch must drop the dial to plaintext, got %q", creds.Info().SecurityProtocol)
	}
}

// @decision: host-agent-proxy-tls
func TestAgentTransportCredentialsRejectAnUnreadableCARoot(t *testing.T) {
	if _, err := agentTransportCredentials(Config{TLSCAPath: filepath.Join(t.TempDir(), "absent.pem")}); err == nil {
		t.Fatal("a CA root the agent cannot read must fail closed rather than fall back to system roots")
	}
}
