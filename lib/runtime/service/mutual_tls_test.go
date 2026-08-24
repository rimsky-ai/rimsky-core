// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
)

const (
	serverKeyID = "server-key-0001"
	clientKeyID = "client-key-0002"
)

func holderFor(t *testing.T, ca *pki.CA, keyID string) *IdentityHolder {
	t.Helper()
	notBefore := time.Now().Add(-time.Hour)
	issued, err := ca.IssueLeaf(keyID, notBefore, 2*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf(%s): %v", keyID, err)
	}
	h := NewIdentityHolder()
	if err := h.Set(issued.CertPEM, issued.KeyPEM, issued.NotBefore, issued.NotAfter); err != nil {
		t.Fatalf("holder.Set(%s): %v", keyID, err)
	}
	return h
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

func TestMutualTLSHandshakeSucceedsUnderDeploymentCA(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	pool := ca.CertPool()
	creds := ClientCredentials{RootCAs: pool, Identity: holderFor(t, ca, clientKeyID)}

	serverCfg := TLSServerConfig(ServiceAuthMTLS, holderFor(t, ca, serverKeyID), pool)
	if serverCfg == nil {
		t.Fatalf("TLSServerConfig(mtls) must not be nil")
	}
	clientCfg := TLSClientConfigFor(creds)
	clientCfg.ServerName = serverKeyID

	clientErr, serverErr := handshake(clientCfg, serverCfg)
	if clientErr != nil || serverErr != nil {
		t.Fatalf("mutual handshake under deployment CA must succeed: client=%v server=%v", clientErr, serverErr)
	}
}

func TestServerRejectsClientCertFromImpostorCA(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	impostor, err := pki.GenerateCA(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	pool := ca.CertPool()
	creds := ClientCredentials{RootCAs: pool, Identity: holderFor(t, impostor, clientKeyID)}

	serverCfg := TLSServerConfig(ServiceAuthMTLS, holderFor(t, ca, serverKeyID), pool)
	clientCfg := TLSClientConfigFor(creds)
	clientCfg.ServerName = serverKeyID

	_, serverErr := handshake(clientCfg, serverCfg)
	if serverErr == nil {
		t.Fatalf("server must reject a client cert signed by an impostor CA")
	}
}

func TestClientRejectsServerCertFromImpostorCA(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	impostor, err := pki.GenerateCA(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	trusted := ca.CertPool()
	creds := ClientCredentials{RootCAs: trusted, Identity: holderFor(t, ca, clientKeyID)}

	serverCfg := TLSServerConfig(ServiceAuthMTLS, holderFor(t, impostor, serverKeyID), impostor.CertPool())
	clientCfg := TLSClientConfigFor(creds)
	clientCfg.ServerName = serverKeyID

	clientErr, _ := handshake(clientCfg, serverCfg)
	if clientErr == nil {
		t.Fatalf("client must reject a server cert signed by an impostor CA")
	}
}

func TestTLSServerConfigNilWhenServiceAuthNone(t *testing.T) {
	if cfg := TLSServerConfig(ServiceAuthNone, NewIdentityHolder(), x509.NewCertPool()); cfg != nil {
		t.Fatalf("TLSServerConfig(none) must be nil (plaintext), got %+v", cfg)
	}
}
