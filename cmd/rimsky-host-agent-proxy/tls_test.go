// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
)

func TestProxyServerCredentialsDisabledByDefault(t *testing.T) {
	creds, err := proxyServerCredentials(Config{})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if creds != nil {
		t.Fatal("no cert/key configured must yield plaintext (nil creds), preserving insecure local-dev default")
	}
}

func TestProxyServerCredentialsRequiresBothPaths(t *testing.T) {
	if _, err := proxyServerCredentials(Config{TLSCertPath: "/tmp/cert.pem"}); err == nil {
		t.Fatal("cert without key must fail")
	}
	if _, err := proxyServerCredentials(Config{TLSKeyPath: "/tmp/key.pem"}); err == nil {
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
	creds, err := proxyServerCredentials(Config{TLSCertPath: certPath, TLSKeyPath: keyPath})
	if err != nil {
		t.Fatalf("load CA-issued leaf: %v", err)
	}
	if creds == nil {
		t.Fatal("a valid cert/key pair must produce server credentials")
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Fatalf("security protocol = %q, want tls", creds.Info().SecurityProtocol)
	}
}
