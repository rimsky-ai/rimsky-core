// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pki

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

func leafCertOf(t *testing.T, issued Issued) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(issued.CertPEM)
	if block == nil {
		t.Fatalf("leaf cert PEM did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	return cert
}

func verifyLeaf(leaf *x509.Certificate, roots *x509.CertPool, usage x509.ExtKeyUsage) error {
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: fixedNow.Add(time.Hour),
		KeyUsages:   []x509.ExtKeyUsage{usage},
	})
	return err
}

func TestLeafAcceptedByIssuingCA_ClientAndServerPaths(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	issued, err := ca.IssueLeaf("key-abc", fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf := leafCertOf(t, issued)
	if err := verifyLeaf(leaf, ca.CertPool(), x509.ExtKeyUsageClientAuth); err != nil {
		t.Fatalf("client-auth verify against issuing CA must succeed: %v", err)
	}
	if err := verifyLeaf(leaf, ca.CertPool(), x509.ExtKeyUsageServerAuth); err != nil {
		t.Fatalf("server-auth verify against issuing CA must succeed: %v", err)
	}
}

func TestLeafRejectedByImpostorCA_ClientAndServerPaths(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	impostor, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	issued, err := ca.IssueLeaf("key-abc", fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf := leafCertOf(t, issued)
	if err := verifyLeaf(leaf, impostor.CertPool(), x509.ExtKeyUsageClientAuth); err == nil {
		t.Fatalf("client-auth verify against impostor CA must fail")
	}
	if err := verifyLeaf(leaf, impostor.CertPool(), x509.ExtKeyUsageServerAuth); err == nil {
		t.Fatalf("server-auth verify against impostor CA must fail")
	}
}

func TestPrincipalFromCertMatchesEnrolledKeyID(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	const keyID = "11111111-2222-3333-4444-555555555555"
	issued, err := ca.IssueLeaf(keyID, fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf := leafCertOf(t, issued)
	got, err := principalFromCert(leaf)
	if err != nil {
		t.Fatalf("principalFromCert: %v", err)
	}
	if got != keyID {
		t.Fatalf("principal mismatch: got %q want %q", got, keyID)
	}
	if issued.NotAfter != fixedNow.Add(LeafTTL) {
		t.Fatalf("NotAfter mismatch: got %v want %v", issued.NotAfter, fixedNow.Add(LeafTTL))
	}
}

func TestLeafValidAtIssuanceInstantAndUnderVerifierClockSkew(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	issued, err := ca.IssueLeaf("key-abc", fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf := leafCertOf(t, issued)
	for _, verifierNow := range []time.Time{fixedNow, fixedNow.Add(-30 * time.Second)} {
		_, verr := leaf.Verify(x509.VerifyOptions{
			Roots:       ca.CertPool(),
			CurrentTime: verifierNow,
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		})
		if verr != nil {
			t.Fatalf("leaf must be valid at verifier clock %v (issued at %v): %v", verifierNow, fixedNow, verr)
		}
	}
}

func TestPrincipalFromCertNoSANReturnsError(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if _, err := principalFromCert(ca.Certificate()); !errors.Is(err, ErrPrincipalNotFound) {
		t.Fatalf("CA cert carries no principal SAN; want ErrPrincipalNotFound, got %v", err)
	}
}

func TestPrincipalFromVerifiedChainsReturnsPrincipalOnlyForVerifiedChain(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	const keyID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	issued, err := ca.IssueLeaf(keyID, fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf := leafCertOf(t, issued)

	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:       ca.CertPool(),
		CurrentTime: fixedNow.Add(time.Hour),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Fatalf("leaf must verify against issuing CA: %v", err)
	}
	got, err := PrincipalFromVerifiedChains(&tls.ConnectionState{VerifiedChains: chains})
	if err != nil {
		t.Fatalf("verified chain must yield a principal: %v", err)
	}
	if got != keyID {
		t.Fatalf("principal mismatch: got %q want %q", got, keyID)
	}
}

func TestPrincipalFromVerifiedChainsRejectsUnverifiedForeignCert(t *testing.T) {
	foreign, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA foreign: %v", err)
	}
	issued, err := foreign.IssueLeaf("attacker-key", fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("foreign IssueLeaf: %v", err)
	}
	spoof := leafCertOf(t, issued)
	if _, err := principalFromCert(spoof); err != nil {
		t.Fatalf("sanity: spoof cert carries a spiffe SAN: %v", err)
	}

	unverified := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{spoof}}
	if _, err := PrincipalFromVerifiedChains(unverified); !errors.Is(err, ErrPrincipalNotFound) {
		t.Fatalf("an unverified peer cert (no VerifiedChains) must not yield a principal; got %v", err)
	}

	if _, err := PrincipalFromVerifiedChains(nil); !errors.Is(err, ErrPrincipalNotFound) {
		t.Fatalf("nil connection state must not yield a principal; got %v", err)
	}
}

func TestCARoundTripThroughLoad(t *testing.T) {
	ca, err := GenerateCA(fixedNow)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	keyDER, err := ca.KeyPKCS8DER()
	if err != nil {
		t.Fatalf("KeyPKCS8DER: %v", err)
	}
	reloaded, err := LoadCA(ca.CertPEM(), keyDER)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	issued, err := reloaded.IssueLeaf("key-xyz", fixedNow, LeafTTL)
	if err != nil {
		t.Fatalf("reloaded IssueLeaf: %v", err)
	}
	leaf := leafCertOf(t, issued)
	if err := verifyLeaf(leaf, ca.CertPool(), x509.ExtKeyUsageClientAuth); err != nil {
		t.Fatalf("leaf issued by reloaded CA must verify against original pool: %v", err)
	}
}
