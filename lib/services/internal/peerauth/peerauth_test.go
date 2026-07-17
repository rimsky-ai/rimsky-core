// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peerauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/mtlstest"
)

const serverName = "rimsky-peer"

func mtlsIdentity(t *testing.T, ca *mtlstest.CA, apiKey string) *Identity {
	t.Helper()
	srv := ca.EnrollServer(serverName, apiKey)
	t.Cleanup(srv.Close)
	cfg := Config{Mode: enroll.PeerAuthMTLS, ControlAPIURL: srv.URL, APIKey: apiKey, Label: serverName}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	id, err := Load(context.Background(), cfg, client, time.Now)
	if err != nil {
		t.Fatalf("Load mtls identity: %v", err)
	}
	return id
}

func handshake(clientCfg, serverCfg *tls.Config) (clientErr, serverErr error) {
	c, s := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		srv := tls.Server(s, serverCfg)
		err := srv.Handshake()
		_ = srv.Close()
		serverDone <- err
	}()
	cli := tls.Client(c, clientCfg)
	clientErr = cli.Handshake()
	_ = cli.Close()
	serverErr = <-serverDone
	return clientErr, serverErr
}

func TestMutualHandshakeSucceeds(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	serverID := mtlsIdentity(t, ca, "srv-key")
	clientID := mtlsIdentity(t, ca, "cli-key")

	clientCfg := clientID.ClientTLSConfig()
	clientCfg.ServerName = serverName

	clientErr, serverErr := handshake(clientCfg, serverID.ServerTLSConfig())
	if serverErr != nil {
		t.Fatalf("server handshake: %v", serverErr)
	}
	if clientErr != nil {
		t.Fatalf("client handshake: %v", clientErr)
	}
}

func TestImpostorCAClientRejected(t *testing.T) {
	realCA, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	impostorCA, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	serverID := mtlsIdentity(t, realCA, "srv-key")
	impostorID := mtlsIdentity(t, impostorCA, "imp-key")

	clientCfg := impostorID.ClientTLSConfig()
	clientCfg.ServerName = serverName
	clientCfg.RootCAs = poolFor(t, realCA)

	_, serverErr := handshake(clientCfg, serverID.ServerTLSConfig())
	if serverErr == nil {
		t.Fatal("expected server to reject impostor-CA client cert")
	}
}

func TestNoClientCertRejected(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	serverID := mtlsIdentity(t, ca, "srv-key")

	clientCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		RootCAs:    poolFor(t, ca),
	}
	_, serverErr := handshake(clientCfg, serverID.ServerTLSConfig())
	if serverErr == nil {
		t.Fatal("expected server to reject client presenting no certificate")
	}
}

func poolFor(t *testing.T, ca *mtlstest.CA) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.RootPEM())) {
		t.Fatal("append ca root")
	}
	return pool
}

func TestNoneModeTLSConfigsNil(t *testing.T) {
	id := &Identity{mode: enroll.PeerAuthNone, now: time.Now}
	if id.Enabled() {
		t.Fatal("none mode must not be Enabled")
	}
	if id.ServerTLSConfig() != nil {
		t.Fatal("ServerTLSConfig must be nil under none")
	}
	if id.ClientTLSConfig() != nil {
		t.Fatal("ClientTLSConfig must be nil under none")
	}
	if id.GRPCServerOptions() != nil {
		t.Fatal("GRPCServerOptions must be nil under none")
	}
}

func TestShouldRenewAtTwoThirdsTTL(t *testing.T) {
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(3 * time.Hour)
	deadline := RenewalDeadline(notBefore, notAfter)
	if !deadline.Equal(notBefore.Add(2 * time.Hour)) {
		t.Fatalf("deadline = %s, want +2h (⅔ of 3h)", deadline)
	}
	cases := []struct {
		now  time.Time
		want bool
	}{
		{notBefore.Add(time.Hour), false},
		{deadline.Add(-time.Nanosecond), false},
		{deadline, true},
		{notAfter, true},
	}
	for _, c := range cases {
		if got := ShouldRenew(notBefore, notAfter, c.now); got != c.want {
			t.Fatalf("ShouldRenew(now=%s) = %v, want %v", c.now, got, c.want)
		}
	}
}

func TestNeedsRenewalUsesInjectedClock(t *testing.T) {
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(3 * time.Hour)
	id := &Identity{mode: enroll.PeerAuthMTLS, cert: &tls.Certificate{}, notBefore: notBefore, notAfter: notAfter}
	if id.NeedsRenewal(notBefore.Add(time.Hour)) {
		t.Fatal("must not need renewal at ⅓ TTL")
	}
	if !id.NeedsRenewal(notBefore.Add(2 * time.Hour)) {
		t.Fatal("must need renewal at ⅔ TTL")
	}
}

func TestNeedsRenewalWhenNoCert(t *testing.T) {
	id := &Identity{mode: enroll.PeerAuthMTLS}
	if !id.NeedsRenewal(time.Now()) {
		t.Fatal("identity with no cert must need renewal")
	}
}

func TestRefreshHotSwapsCert(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	id := mtlsIdentity(t, ca, "swap-key")
	first, err := id.getCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := id.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := id.getCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("refresh must hot-swap to a new *tls.Certificate")
	}
}

func TestLoadMTLSFailsClosedWithoutInputs(t *testing.T) {
	_, err := Load(context.Background(), Config{Mode: enroll.PeerAuthMTLS, Label: "x"}, nil, time.Now)
	if err == nil {
		t.Fatal("mtls without control-api url / api key must fail closed")
	}
}

func TestLoadMTLSFailsClosedWhenEnrollUnreachable(t *testing.T) {
	cfg := Config{Mode: enroll.PeerAuthMTLS, ControlAPIURL: "http://127.0.0.1:1", APIKey: "k", Label: "x"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Load(ctx, cfg, nil, time.Now); err == nil {
		t.Fatal("unreachable enroll endpoint must fail closed at startup")
	}
}

func TestLoadNoneReturnsDisabledIdentity(t *testing.T) {
	id, err := Load(context.Background(), Config{Mode: enroll.PeerAuthNone}, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if id.Enabled() {
		t.Fatal("none identity must be disabled")
	}
}

func TestLoadConfigFromEnvDefaultsToNone(t *testing.T) {
	t.Setenv(EnvPeerAuth, "")
	cfg, err := LoadConfigFromEnv("svc")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != enroll.PeerAuthNone {
		t.Fatalf("mode = %q, want none", cfg.Mode)
	}
}

func TestLoadConfigFromEnvRejectsBogusMode(t *testing.T) {
	t.Setenv(EnvPeerAuth, "on")
	if _, err := LoadConfigFromEnv("svc"); err == nil {
		t.Fatal("bogus RIMSKY_PEER_AUTH must error")
	}
}

func TestLoadConfigFromEnvFallsBackToEndpoint(t *testing.T) {
	t.Setenv(EnvPeerAuth, enroll.PeerAuthMTLS)
	t.Setenv(EnvControlAPIURL, "")
	t.Setenv(EnvEndpoint, "http://control:8080")
	t.Setenv(EnvAPIKey, "k")
	cfg, err := LoadConfigFromEnv("svc")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlAPIURL != "http://control:8080" {
		t.Fatalf("ControlAPIURL = %q, want RIMSKY_ENDPOINT fallback", cfg.ControlAPIURL)
	}
}
