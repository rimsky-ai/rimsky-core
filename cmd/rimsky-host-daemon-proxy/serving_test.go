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
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
)

// @decision: host-daemon-proxy-enrollment
func enrolledProxyIdentity(t *testing.T, ca *mtlstest.CA) *serviceauth.Identity {
	t.Helper()
	srv := ca.EnrollServer(enroll.ServiceServerName, "proxy-key")
	t.Cleanup(srv.Close)
	id, err := serviceauth.Load(context.Background(), serviceauth.Config{
		Mode: enroll.ServiceAuthMTLS, ControlAPIURL: srv.URL, APIKey: "proxy-key", Label: "host-daemon-proxy",
	}, &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}, time.Now)
	if err != nil {
		t.Fatalf("serviceauth.Load: %v", err)
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
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: enroll.ServiceServerName}
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
	if servers.service == nil {
		t.Fatal("mtls mode must produce a distinct service-facing server")
	}
	addr := serveOnEphemeral(t, servers.service)

	ctx, cancel := context.WithCancel(context.Background())
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

func TestSplitServing_DaemonListenerCarriesOnlyHostDaemon(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	identity := enrolledProxyIdentity(t, ca)
	servers := buildProxyServers(Config{}, newProxyState(), identity, &http.Client{}, nil)

	daemonServices := servers.daemon.GetServiceInfo()
	if _, ok := daemonServices["rimsky.v1.HostDaemon"]; !ok {
		t.Fatalf("daemon listener must serve HostDaemon; got %v", serviceNames(daemonServices))
	}
	for name := range daemonServices {
		if name != "rimsky.v1.HostDaemon" {
			t.Fatalf("under mtls the daemon listener must carry ONLY HostDaemon; found %q", name)
		}
	}
	supServices := servers.service.GetServiceInfo()
	for _, want := range []string{"rimsky.v1.Executor", "rimsky.v1.ClaimProducer", "rimsky.v1.LifecycleSubscriber"} {
		if _, ok := supServices[want]; !ok {
			t.Fatalf("supervisor listener must serve %s; got %v", want, serviceNames(supServices))
		}
	}
}

// @decision: host-daemon-proxy-tls
// @concept: host-daemon-proxy
func TestSplitServing_HoldsWithServiceAuthOff(t *testing.T) {
	identity, err := serviceauth.Load(context.Background(), serviceauth.Config{Mode: enroll.ServiceAuthNone}, nil, time.Now)
	if err != nil {
		t.Fatalf("serviceauth.Load: %v", err)
	}
	servers := buildProxyServers(Config{}, newProxyState(), identity, &http.Client{}, nil)
	if servers.service == nil {
		t.Fatal("the service-facing listener stands in every posture, so a plaintext service keeps a port of its own")
	}
	daemonServices := servers.daemon.GetServiceInfo()
	for name := range daemonServices {
		if name != "rimsky.v1.HostDaemon" {
			t.Fatalf("the daemon listener carries only HostDaemon, whatever the service-auth posture; found %q", name)
		}
	}
	if _, ok := daemonServices["rimsky.v1.HostDaemon"]; !ok {
		t.Fatalf("daemon listener must serve HostDaemon; got %v", serviceNames(daemonServices))
	}
	serviceFacingInfo := servers.service.GetServiceInfo()
	for _, want := range []string{"rimsky.v1.Executor", "rimsky.v1.ClaimProducer", "rimsky.v1.LifecycleSubscriber"} {
		if _, ok := serviceFacingInfo[want]; !ok {
			t.Fatalf("service-facing listener must serve %s; got %v", want, serviceNames(serviceFacingInfo))
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
