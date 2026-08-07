// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/mtlstest"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
)

const bridgeServerName = "http-node"

func mtlsBridgeIdentity(t *testing.T, ca *mtlstest.CA) *peerauth.Identity {
	t.Helper()
	enrollSrv := ca.EnrollServer(bridgeServerName, "bridge-key")
	t.Cleanup(enrollSrv.Close)
	cfg := peerauth.Config{
		Mode:          enroll.PeerAuthMTLS,
		ControlAPIURL: enrollSrv.URL,
		APIKey:        "bridge-key",
		Label:         bridgeServerName,
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	id, err := peerauth.Load(context.Background(), cfg, client, time.Now)
	if err != nil {
		t.Fatalf("load mtls bridge identity: %v", err)
	}
	return id
}

func serveBridge(t *testing.T, s *Server, id *peerauth.Identity) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	MountBridge(mux, s)
	srv := NewBridgeServer(lis.Addr().String(), mux, id)
	go func() { _ = ServeBridge(srv, lis, id) }()
	t.Cleanup(func() { _ = srv.Close() })
	return lis.Addr().String()
}

func executeBody(t *testing.T) []byte {
	t.Helper()
	body, err := protojson.Marshal(newRequest(t, map[string]any{"stub_response": map[string]any{"ok": true}}))
	if err != nil {
		t.Fatalf("marshal execute request: %v", err)
	}
	return body
}

func mtlsClient(t *testing.T, ca *mtlstest.CA, withClientCert bool) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.RootPEM())) {
		t.Fatal("append ca root")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: bridgeServerName}
	if withClientCert {
		certPEM, keyPEM, err := ca.Leaf("rimsky-client", time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			t.Fatal(err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}}
}

func TestBridgeMTLS_AcceptsMutuallyAuthedClient(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	addr := serveBridge(t, testServer(t, true), mtlsBridgeIdentity(t, ca))
	client := mtlsClient(t, ca, true)

	req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/Execute", bytes.NewReader(executeBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("mutually-authenticated request must succeed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBridgeMTLS_RejectsNoClientCert(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	addr := serveBridge(t, testServer(t, true), mtlsBridgeIdentity(t, ca))
	client := mtlsClient(t, ca, false)

	req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/Execute", bytes.NewReader(executeBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("bridge under mtls must reject a client presenting no certificate")
	}
}

func TestBridgeMTLS_RejectsPlaintext(t *testing.T) {
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	addr := serveBridge(t, testServer(t, true), mtlsBridgeIdentity(t, ca))

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/Execute", bytes.NewReader(executeBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("bridge under mtls must not dispatch a plaintext request")
	}
}

func TestBridgeNone_ServesPlaintext(t *testing.T) {
	id, err := peerauth.Load(context.Background(), peerauth.Config{Mode: enroll.PeerAuthNone}, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	addr := serveBridge(t, testServer(t, true), id)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/Execute", bytes.NewReader(executeBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("under none the bridge must serve plaintext: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
