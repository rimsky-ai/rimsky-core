// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/mtlstest"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @decision: host-agent-proxy-enrollment
func enrolledProxyIdentity(t *testing.T, ca *mtlstest.CA) *peerauth.Identity {
	t.Helper()
	srv := ca.EnrollServer(enroll.PeerServerName, "proxy-key")
	t.Cleanup(srv.Close)
	id, err := peerauth.Load(context.Background(), peerauth.Config{
		Mode: enroll.PeerAuthMTLS, ControlAPIURL: srv.URL, APIKey: "proxy-key", Label: "host-agent-proxy",
	}, &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}, time.Now)
	if err != nil {
		t.Fatalf("peerauth.Load: %v", err)
	}
	return id
}

func serveOnEphemeral(t *testing.T, srv *grpc.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func clientTLS(t *testing.T, ca *mtlstest.CA, withClientCert bool) credentials.TransportCredentials {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.RootPEM())) {
		t.Fatal("bad CA root")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: enroll.PeerServerName}
	if withClientCert {
		certPEM, keyPEM, err := ca.Leaf("supervisor", time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("leaf: %v", err)
		}
		pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			t.Fatalf("keypair: %v", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return credentials.NewTLS(cfg)
}

func TestSplitServing_SupervisorListenerRequiresClientCert(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	identity := enrolledProxyIdentity(t, ca)
	servers := buildProxyServers(Config{}, newProxyState(), identity, &http.Client{}, nil)
	if servers.peer == nil {
		t.Fatal("mtls mode must produce a distinct peer-facing server")
	}
	addr := serveOnEphemeral(t, servers.peer)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authed, err := grpc.NewClient(addr, grpc.WithTransportCredentials(clientTLS(t, ca, true)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer authed.Close()
	if _, err := genv1.NewClaimProducerClient(authed).Capabilities(ctx, &genv1.CapabilitiesRequest{}); err != nil {
		t.Fatalf("a CA-issued client cert must be accepted on the supervisor listener: %v", err)
	}

	unauthed, err := grpc.NewClient(addr, grpc.WithTransportCredentials(clientTLS(t, ca, false)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer unauthed.Close()
	if _, err := genv1.NewClaimProducerClient(unauthed).Capabilities(ctx, &genv1.CapabilitiesRequest{}); err == nil {
		t.Fatal("a client without a certificate must be refused by the supervisor listener")
	}
}

func TestSplitServing_AgentListenerCarriesOnlyHostAgent(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	identity := enrolledProxyIdentity(t, ca)
	servers := buildProxyServers(Config{}, newProxyState(), identity, &http.Client{}, nil)

	agentServices := servers.agent.GetServiceInfo()
	if _, ok := agentServices["rimsky.v1.HostAgent"]; !ok {
		t.Fatalf("agent listener must serve HostAgent; got %v", serviceNames(agentServices))
	}
	for name := range agentServices {
		if name != "rimsky.v1.HostAgent" {
			t.Fatalf("under mtls the agent listener must carry ONLY HostAgent; found %q", name)
		}
	}
	supServices := servers.peer.GetServiceInfo()
	for _, want := range []string{"rimsky.v1.Executor", "rimsky.v1.ClaimProducer", "rimsky.v1.LifecycleSubscriber"} {
		if _, ok := supServices[want]; !ok {
			t.Fatalf("supervisor listener must serve %s; got %v", want, serviceNames(supServices))
		}
	}
}

func TestSingleServing_PeerAuthNoneKeepsOneServer(t *testing.T) {
	identity, err := peerauth.Load(context.Background(), peerauth.Config{Mode: enroll.PeerAuthNone}, nil, time.Now)
	if err != nil {
		t.Fatalf("peerauth.Load: %v", err)
	}
	servers := buildProxyServers(Config{}, newProxyState(), identity, &http.Client{}, nil)
	if servers.peer != nil {
		t.Fatal("peer-auth none must keep the single-listener shape")
	}
	agentServices := servers.agent.GetServiceInfo()
	for _, want := range []string{"rimsky.v1.HostAgent", "rimsky.v1.Executor", "rimsky.v1.ClaimProducer"} {
		if _, ok := agentServices[want]; !ok {
			t.Fatalf("single-listener shape must serve %s; got %v", want, serviceNames(agentServices))
		}
	}
}

func serviceNames(m map[string]grpc.ServiceInfo) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	return out
}
