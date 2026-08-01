// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
)

func leafCert(t *testing.T, ca *pki.CA, principal string) *x509.Certificate {
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

func TestVerifiedPeerPrincipal_ExtractsFromVerifiedChain(t *testing.T) {
	ca, err := pki.GenerateCA(time.Now())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	server := leafCert(t, ca, "executor-server-1")
	info := credentials.TLSInfo{
		State: tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{server, ca.Certificate()}},
		},
	}
	if got := verifiedPeerPrincipal(info); got != "executor-server-1" {
		t.Fatalf("verifiedPeerPrincipal = %q, want the dispatched executor's verified principal", got)
	}
}

func TestVerifiedPeerPrincipal_EmptyWithoutTLS(t *testing.T) {
	if got := verifiedPeerPrincipal(nil); got != "" {
		t.Fatalf("verifiedPeerPrincipal(nil auth info) = %q, want \"\" (plaintext dispatch has no principal)", got)
	}
	if got := verifiedPeerPrincipal(credentials.TLSInfo{}); got != "" {
		t.Fatalf("verifiedPeerPrincipal(no verified chains) = %q, want \"\"", got)
	}
}
