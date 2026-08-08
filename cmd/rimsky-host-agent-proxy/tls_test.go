// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/mtlstest"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
)

func TestProxyServerCredentialsDisabledByDefault(t *testing.T) {
	creds, _, err := proxyServerCredentials(Config{}, nil)
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if creds != nil {
		t.Fatal("no cert/key configured must yield plaintext (nil creds), preserving insecure local-dev default")
	}
}

func TestProxyServerCredentialsRequiresBothPaths(t *testing.T) {
	if _, _, err := proxyServerCredentials(Config{TLSCertPath: "/tmp/cert.pem"}, nil); err == nil {
		t.Fatal("cert without key must fail")
	}
	if _, _, err := proxyServerCredentials(Config{TLSKeyPath: "/tmp/key.pem"}, nil); err == nil {
		t.Fatal("key without cert must fail")
	}
}

func TestProxyServerCredentialsLoadsCAIssuedLeaf(t *testing.T) {
	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	issued, err := ca.IssueLeaf("proxy-host", time.Now().Add(-time.Hour), 2*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, issued.CertPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, issued.KeyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	creds, source, err := proxyServerCredentials(Config{TLSCertPath: certPath, TLSKeyPath: keyPath}, nil)
	if err != nil {
		t.Fatalf("load CA-issued leaf: %v", err)
	}
	if creds == nil {
		t.Fatal("a valid cert/key pair must produce server credentials")
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Fatalf("security protocol = %q, want tls", creds.Info().SecurityProtocol)
	}
	if !strings.Contains(source, certPath) {
		t.Fatalf("credential source = %q, want it to name the operator-mounted keypair", source)
	}
}

// @concept: peer-auth
// @concept: host-agent-proxy
func TestProxyServerCredentialsUseEnrolledLeafUnderMutualTLS(t *testing.T) {
	identity := enrolledIdentityForTest(t)

	creds, source, err := proxyServerCredentials(Config{}, identity)
	if err != nil {
		t.Fatalf("enrolled-leaf credentials: %v", err)
	}
	if creds == nil {
		t.Fatal("under mutual TLS the agent hop must be served with the leaf the proxy already enrolls, not in plaintext")
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Fatalf("security protocol = %q, want tls", creds.Info().SecurityProtocol)
	}
	if source != "enrolled deployment-CA leaf" {
		t.Fatalf("credential source = %q, want the enrolled leaf", source)
	}
}

// @concept: peer-auth
func TestProxyServerCredentialsPreferOperatorMountedKeypair(t *testing.T) {
	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	issued, err := ca.IssueLeaf("proxy-host", time.Now().Add(-time.Hour), 2*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, issued.CertPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, issued.KeyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	_, source, err := proxyServerCredentials(Config{TLSCertPath: certPath, TLSKeyPath: keyPath}, enrolledIdentityForTest(t))
	if err != nil {
		t.Fatalf("operator keypair with enrollment present: %v", err)
	}
	if !strings.Contains(source, certPath) {
		t.Fatalf("an explicitly mounted keypair must win over the enrolled leaf; source = %q", source)
	}
}

// @concept: peer-auth
func enrolledIdentityForTest(t *testing.T) *peerauth.Identity {
	t.Helper()
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatalf("mtlstest.NewCA: %v", err)
	}
	srv := ca.EnrollServer("host-agent-proxy", "test-key")
	t.Cleanup(srv.Close)

	identity, err := peerauth.Load(context.Background(), peerauth.Config{
		Mode:                     enroll.PeerAuthMTLS,
		ControlAPIURL:            srv.URL,
		APIKey:                   "test-key",
		Label:                    "host-agent-proxy",
		AllowPlaintextEnrollment: true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("peerauth.Load: %v", err)
	}
	return identity
}
