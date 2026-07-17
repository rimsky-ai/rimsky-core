// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func clientCertPresented(t *testing.T) bool {
	t.Helper()
	cfg := peer.TLSClientConfig()
	if cfg.GetClientCertificate == nil {
		return false
	}
	cert, err := cfg.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatalf("GetClientCertificate: %v", err)
	}
	return len(cert.Certificate) > 0
}

func TestOutboundIdentity_MTLSPresentsClientCert(t *testing.T) {
	t.Cleanup(func() { peer.SetClientIdentity(nil) })

	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	holder, err := setupOutboundIdentity(ctx, ca, "supervisor-42", shared.SystemClock{}, shared.SilentLogger{})
	if err != nil {
		t.Fatalf("setupOutboundIdentity: %v", err)
	}
	if !holder.HasIdentity() {
		t.Fatal("holder must carry an identity after setup")
	}
	if !clientCertPresented(t) {
		t.Fatal("under mtls the outbound TLSClientConfig() must present a client certificate")
	}
}

func serverLeaf(t *testing.T, ca *pki.CA, principal string) *x509.Certificate {
	t.Helper()
	issued, err := ca.IssueLeaf(principal, time.Now().Add(-time.Hour), 2*time.Hour)
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

func TestOutboundIdentity_MTLSPinsDeploymentRootCAs(t *testing.T) {
	t.Cleanup(func() { peer.SetClientIdentity(nil); peer.SetTLSRootCAs(nil) })

	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if _, err := setupOutboundIdentity(ctx, ca, "supervisor-42", shared.SystemClock{}, shared.SilentLogger{}); err != nil {
		t.Fatalf("setupOutboundIdentity: %v", err)
	}

	cfg := peer.TLSClientConfig()
	if cfg.RootCAs == nil {
		t.Fatal("production mtls wiring must pin the deployment CA into RootCAs (nil means core verifies peers against the system pool)")
	}

	verifyOpts := x509.VerifyOptions{Roots: cfg.RootCAs, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, err := serverLeaf(t, ca, "server-1").Verify(verifyOpts); err != nil {
		t.Fatalf("a deployment-CA server cert must verify against the pinned pool: %v", err)
	}

	impostor, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	if _, err := serverLeaf(t, impostor, "server-1").Verify(verifyOpts); err == nil {
		t.Fatal("a cert outside the deployment CA must NOT verify against the pinned pool (system-pool trust was the widened MITM surface)")
	}
}

func TestOutboundIdentity_NonePresentsNoClientCert(t *testing.T) {
	t.Cleanup(func() { peer.SetClientIdentity(nil) })
	peer.SetClientIdentity(nil)

	if clientCertPresented(t) {
		t.Fatal("under none the outbound TLSClientConfig() must not present a client certificate")
	}
}
