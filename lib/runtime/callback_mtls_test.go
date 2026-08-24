// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

func issueClientPair(t *testing.T, ca *pki.CA, principal string) tls.Certificate {
	t.Helper()
	issued, err := ca.IssueLeaf(principal, time.Now().Add(-time.Hour), 48*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf(%s): %v", principal, err)
	}
	pair, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return pair
}

func startMTLSCallbackServer(t *testing.T, ca *pki.CA) (*CallbackServer, string) {
	t.Helper()
	serverIssued, err := ca.IssueLeaf("supervisor-mtls", time.Now().Add(-time.Hour), 48*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf server: %v", err)
	}
	serverIdentity := service.NewIdentityHolder()
	if err := serverIdentity.Set(serverIssued.CertPEM, serverIssued.KeyPEM, serverIssued.NotBefore, serverIssued.NotAfter); err != nil {
		t.Fatalf("serverIdentity.Set: %v", err)
	}
	cb := &CallbackServer{
		Registry:       NewCallbackRegistry(),
		Logger:         shared.SilentLogger{},
		SupervisorID:   "supervisor-mtls",
		ServiceAuth:    service.ServiceAuthMTLS,
		ServerIdentity: serverIdentity,
		ClientCAs:      ca.CertPool(),
	}
	addr, err := cb.Start("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("cb.Start: %v", err)
	}
	t.Cleanup(func() { _ = cb.Close(context.Background()) })
	return cb, addr
}

func mtlsClient(rootPool *x509.CertPool, clientCert *tls.Certificate) *http.Client {
	cfg := &tls.Config{
		RootCAs:    rootPool,
		ServerName: "supervisor-mtls",
		MinVersion: tls.VersionTLS12,
	}
	if clientCert != nil {
		cfg.Certificates = []tls.Certificate{*clientCert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
}

func postCallback(client *http.Client, addr, ackID, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/callback/"+ackID, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

func TestCallbackMTLS_ValidClientCertAccepted(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cb, addr := startMTLSCallbackServer(t, ca)

	ackID := "ack-" + uuid.NewString()
	cb.Registry.Register(ackID, AsyncContext{NodeRunID: shared.UUID(uuid.New())})

	clientCert := issueClientPair(t, ca, "executor-1")
	client := mtlsClient(ca.CertPool(), &clientCert)

	resp, err := postCallback(client, addr, ackID, `{}`)
	if err != nil {
		t.Fatalf("handshake with valid client cert must succeed: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (verified service, ackID matched registry, body has no outcome)", resp.StatusCode)
	}
}

func TestCallbackMTLS_KnownAckCorrelatesUnknownAckIs404(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cb, addr := startMTLSCallbackServer(t, ca)

	known := "ack-" + uuid.NewString()
	cb.Registry.Register(known, AsyncContext{NodeRunID: shared.UUID(uuid.New())})

	clientCert := issueClientPair(t, ca, "executor-1")
	client := mtlsClient(ca.CertPool(), &clientCert)

	knownResp, err := postCallback(client, addr, known, `{}`)
	if err != nil {
		t.Fatalf("known ackID request: %v", err)
	}
	_, _ = io.Copy(io.Discard, knownResp.Body)
	_ = knownResp.Body.Close()
	if knownResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("known ackID status = %d, want 400 (ackID popped from registry, then body-parse rejected)", knownResp.StatusCode)
	}

	unknownResp, err := postCallback(client, addr, "ack-"+uuid.NewString(), `{}`)
	if err != nil {
		t.Fatalf("unknown ackID request: %v", err)
	}
	_, _ = io.Copy(io.Discard, unknownResp.Body)
	_ = unknownResp.Body.Close()
	if unknownResp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown ackID status = %d, want 404 (async_ack_id is the correlation key)", unknownResp.StatusCode)
	}
}

func TestCallbackServer_Start_MTLSRequiresClientCAs(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	serverIssued, err := ca.IssueLeaf("supervisor-mtls", time.Now().Add(-time.Hour), 48*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	serverIdentity := service.NewIdentityHolder()
	if err := serverIdentity.Set(serverIssued.CertPEM, serverIssued.KeyPEM, serverIssued.NotBefore, serverIssued.NotAfter); err != nil {
		t.Fatalf("serverIdentity.Set: %v", err)
	}
	cb := &CallbackServer{
		Registry:       NewCallbackRegistry(),
		Logger:         shared.SilentLogger{},
		SupervisorID:   "supervisor-mtls",
		ServiceAuth:    service.ServiceAuthMTLS,
		ServerIdentity: serverIdentity,
		ClientCAs:      nil,
	}
	_, err = cb.Start("127.0.0.1", 0)
	if err == nil {
		_ = cb.Close(context.Background())
		t.Fatal("Start under mtls with nil ClientCAs must return an error (RequireAndVerifyClientCert against a nil pool would verify nothing)")
	}
	if !strings.Contains(err.Error(), "client CA") {
		t.Fatalf("error %q must name the missing client CA pool", err)
	}
}

func parseLeafCert(t *testing.T, ca *pki.CA, principal string) *x509.Certificate {
	t.Helper()
	issued, err := ca.IssueLeaf(principal, time.Now().Add(-time.Hour), 48*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf(%s): %v", principal, err)
	}
	block, _ := pem.Decode(issued.CertPEM)
	if block == nil {
		t.Fatalf("pem.Decode(%s): no block", principal)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate(%s): %v", principal, err)
	}
	return cert
}

func requestWithServiceCert(cert *x509.Certificate) *http.Request {
	r := &http.Request{}
	if cert != nil {
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
			VerifiedChains:   [][]*x509.Certificate{{cert}},
		}
	}
	return r
}

func TestEnforceCallbackPrincipal(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	dispatched := parseLeafCert(t, ca, "executor-1")
	other := parseLeafCert(t, ca, "executor-2")

	mtls := &CallbackServer{ServiceAuth: service.ServiceAuthMTLS}
	none := &CallbackServer{ServiceAuth: service.ServiceAuthNone}

	if err := mtls.enforceCallbackPrincipal(requestWithServiceCert(dispatched), AsyncContext{AsyncAckPrincipal: "executor-1"}); err != nil {
		t.Fatalf("matching callback principal must be accepted: %v", err)
	}
	if err := mtls.enforceCallbackPrincipal(requestWithServiceCert(other), AsyncContext{AsyncAckPrincipal: "executor-1"}); err == nil {
		t.Fatal("callback principal differing from the dispatched principal must be rejected")
	}
	if err := mtls.enforceCallbackPrincipal(requestWithServiceCert(dispatched), AsyncContext{AsyncAckPrincipal: ""}); err != nil {
		t.Fatalf("no stored principal must skip the binding check: %v", err)
	}
	if err := none.enforceCallbackPrincipal(requestWithServiceCert(other), AsyncContext{AsyncAckPrincipal: "executor-1"}); err != nil {
		t.Fatalf("under service_auth none there are no principals; the check must be skipped: %v", err)
	}
}

func TestCallbackMTLS_PrincipalMismatchRejected(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cb, addr := startMTLSCallbackServer(t, ca)

	ackID := "ack-" + uuid.NewString()
	cb.Registry.Register(ackID, AsyncContext{NodeRunID: shared.UUID(uuid.New()), AsyncAckPrincipal: "executor-1"})

	wrongCert := issueClientPair(t, ca, "executor-2")
	client := mtlsClient(ca.CertPool(), &wrongCert)

	resp, err := postCallback(client, addr, ackID, `{}`)
	if err != nil {
		t.Fatalf("handshake with a valid deployment cert must succeed: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (callback cert principal executor-2 != dispatched executor-1)", resp.StatusCode)
	}
}

func TestCallbackMTLS_PrincipalMatchPassesBinding(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cb, addr := startMTLSCallbackServer(t, ca)

	ackID := "ack-" + uuid.NewString()
	cb.Registry.Register(ackID, AsyncContext{NodeRunID: shared.UUID(uuid.New()), AsyncAckPrincipal: "executor-1"})

	matchCert := issueClientPair(t, ca, "executor-1")
	client := mtlsClient(ca.CertPool(), &matchCert)

	resp, err := postCallback(client, addr, ackID, `{}`)
	if err != nil {
		t.Fatalf("handshake must succeed: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (matching principal clears the binding gate, then empty body fails parse)", resp.StatusCode)
	}
}

func TestCallbackMTLS_NoClientCertRejectedAtTLS(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	_, addr := startMTLSCallbackServer(t, ca)

	client := mtlsClient(ca.CertPool(), nil)
	resp, err := postCallback(client, addr, "ack-"+uuid.NewString(), `{}`)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("request with no client cert must be rejected at the TLS layer, got status %d", resp.StatusCode)
	}
}

func TestCallbackMTLS_ImpostorCARejectedAtTLS(t *testing.T) {
	t.Parallel()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	_, addr := startMTLSCallbackServer(t, ca)

	impostor, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	impostorCert := issueClientPair(t, impostor, "executor-evil")
	client := mtlsClient(ca.CertPool(), &impostorCert)

	resp, err := postCallback(client, addr, "ack-"+uuid.NewString(), `{}`)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("client cert from an impostor CA must be rejected at the TLS layer, got status %d", resp.StatusCode)
	}
}
