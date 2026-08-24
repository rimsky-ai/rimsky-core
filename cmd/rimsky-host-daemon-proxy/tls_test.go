// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/mtlstest"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
)

// @decision: host-daemon-proxy-tls
func TestProxyServerCredentialsMintALocalCAWhenNothingElseIsConfigured(t *testing.T) {
	got, err := proxyServerCredentials(Config{}, nil, time.Now)
	if err != nil {
		t.Fatalf("zero-config credentials: %v", err)
	}
	if got.Credentials == nil {
		t.Fatal("the daemon-facing hop serves TLS in the zero-config posture")
	}
	if got.Credentials.Info().SecurityProtocol != "tls" {
		t.Fatalf("security protocol = %q, want tls", got.Credentials.Info().SecurityProtocol)
	}
	if len(got.LocalCAPEM) == 0 {
		t.Fatal("the zero-config posture publishes the CA root the daemon pins")
	}
	if _, err := enroll.CAPoolFromPEM("local", got.LocalCAPEM); err != nil {
		t.Fatalf("published CA root does not parse: %v", err)
	}
}

// @decision: host-daemon-proxy-tls
func TestProxyServerCredentialsPlaintextOnlyBehindTheInsecureSwitch(t *testing.T) {
	got, err := proxyServerCredentials(Config{Insecure: true}, nil, time.Now)
	if err != nil {
		t.Fatalf("insecure switch: %v", err)
	}
	if got.Credentials != nil {
		t.Fatal("the insecure switch must drop the daemon-facing hop to plaintext")
	}
	if !strings.Contains(got.Source, envInsecureHop) {
		t.Fatalf("credential source = %q, want it to name the switch that chose plaintext", got.Source)
	}
}

func TestProxyServerCredentialsRequiresBothPaths(t *testing.T) {
	if _, err := proxyServerCredentials(Config{TLSCertPath: "/tmp/cert.pem"}, nil, time.Now); err == nil {
		t.Fatal("cert without key must fail")
	}
	if _, err := proxyServerCredentials(Config{TLSKeyPath: "/tmp/key.pem"}, nil, time.Now); err == nil {
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
	got, err := proxyServerCredentials(Config{TLSCertPath: certPath, TLSKeyPath: keyPath}, nil, time.Now)
	if err != nil {
		t.Fatalf("load CA-issued leaf: %v", err)
	}
	if got.Credentials == nil {
		t.Fatal("a valid cert/key pair must produce server credentials")
	}
	if got.Credentials.Info().SecurityProtocol != "tls" {
		t.Fatalf("security protocol = %q, want tls", got.Credentials.Info().SecurityProtocol)
	}
	if !strings.Contains(got.Source, certPath) {
		t.Fatalf("credential source = %q, want it to name the operator-mounted keypair", got.Source)
	}
}

// @concept: service-auth
// @concept: host-daemon-proxy
func TestProxyServerCredentialsUseEnrolledLeafUnderMutualTLS(t *testing.T) {
	identity := enrolledIdentityForTest(t)

	got, err := proxyServerCredentials(Config{}, identity, time.Now)
	if err != nil {
		t.Fatalf("enrolled-leaf credentials: %v", err)
	}
	if got.Credentials == nil {
		t.Fatal("under mutual TLS the daemon hop must be served with the leaf the proxy already enrolls, not in plaintext")
	}
	if got.Credentials.Info().SecurityProtocol != "tls" {
		t.Fatalf("security protocol = %q, want tls", got.Credentials.Info().SecurityProtocol)
	}
	if got.Source != "enrolled deployment-CA leaf" {
		t.Fatalf("credential source = %q, want the enrolled leaf", got.Source)
	}
}

// @concept: service-auth
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

	got, err := proxyServerCredentials(Config{TLSCertPath: certPath, TLSKeyPath: keyPath}, enrolledIdentityForTest(t), time.Now)
	if err != nil {
		t.Fatalf("operator keypair with enrollment present: %v", err)
	}
	if !strings.Contains(got.Source, certPath) {
		t.Fatalf("an explicitly mounted keypair must win over the enrolled leaf; source = %q", got.Source)
	}
}

// @concept: service-auth
func enrolledIdentityForTest(t *testing.T) *serviceauth.Identity {
	t.Helper()
	ca, err := mtlstest.NewCA()
	if err != nil {
		t.Fatalf("mtlstest.NewCA: %v", err)
	}
	srv := ca.EnrollServer("host-daemon-proxy", "test-key")
	t.Cleanup(srv.Close)

	identity, err := serviceauth.Load(context.Background(), serviceauth.Config{
		Mode:                     enroll.ServiceAuthMTLS,
		ControlAPIURL:            srv.URL,
		APIKey:                   "test-key",
		Label:                    "host-daemon-proxy",
		AllowPlaintextEnrollment: true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("serviceauth.Load: %v", err)
	}
	return identity
}

// @decision: host-daemon-proxy-tls
func TestLocalLeafHolderReissuesTheLeafBeforeItExpires(t *testing.T) {
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	at := start
	holder, err := newLocalLeafHolder(func() time.Time { return at }, "")
	if err != nil {
		t.Fatalf("newLocalLeafHolder: %v", err)
	}

	first := servedLeafSerial(t, holder)

	at = start.Add(pki.LeafTTL / 2)
	if got := servedLeafSerial(t, holder); got != first {
		t.Fatalf("a leaf well inside its lifetime must be served as issued; serial %s became %s", first, got)
	}

	at = serviceauth.RenewalDeadline(start, start.Add(pki.LeafTTL))
	renewed := servedLeafSerial(t, holder)
	if renewed == first {
		t.Fatalf("a leaf past its renewal deadline must be replaced; serial %s was served again", first)
	}
}

// @decision: host-daemon-proxy-tls
func TestLocalLeafHolderRenewsUnderTheCARootItPublished(t *testing.T) {
	at := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	holder, err := newLocalLeafHolder(func() time.Time { return at }, "")
	if err != nil {
		t.Fatalf("newLocalLeafHolder: %v", err)
	}
	pool, err := enroll.CAPoolFromPEM("local", holder.ca.CertPEM())
	if err != nil {
		t.Fatalf("published CA root does not parse: %v", err)
	}

	at = at.Add(pki.LeafTTL)
	cert, err := holder.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse renewed leaf: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at, DNSName: enroll.ServiceServerName}); err != nil {
		t.Fatalf("the renewed leaf must chain to the root the daemon pinned at startup: %v", err)
	}
}

func servedLeafSerial(t *testing.T, holder *localLeafHolder) string {
	t.Helper()
	cert, err := holder.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse served leaf: %v", err)
	}
	return leaf.SerialNumber.String()
}
