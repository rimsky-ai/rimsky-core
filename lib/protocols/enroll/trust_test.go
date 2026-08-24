// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package enroll

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return &testCA{cert: cert, key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca *testCA) leaf(t *testing.T, names ...string) *x509.Certificate {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &leafKey.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	return leaf
}

func (ca *testCA) pool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool, err := CAPoolFromPEM("test", ca.certPEM)
	if err != nil {
		t.Fatalf("CAPoolFromPEM: %v", err)
	}
	return pool
}

func TestPinnedTLSConfigVerifiesAServiceByItsIssuerAndTheFixedServiceName(t *testing.T) {
	ca := newTestCA(t)
	cfg := PinnedTLSConfig(ca.pool(t))
	if cfg.ServerName != ServiceServerName {
		t.Fatalf("ServerName = %q, want %q — an enrolled leaf is named by its workload principal, "+
			"never by the host the config happens to point at", cfg.ServerName, ServiceServerName)
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("verification must stay on: the fixed service name exists so the standard chain check still runs")
	}
	leaf := ca.leaf(t, "key-01JWORKLOAD", ServiceServerName)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: cfg.RootCAs, DNSName: cfg.ServerName}); err != nil {
		t.Fatalf("a CA-issued service leaf must verify under the pinned config: %v", err)
	}
}

func TestPinnedTLSConfigRejectsALeafFromAnotherCA(t *testing.T) {
	pinned := newTestCA(t)
	impostor := newTestCA(t)
	cfg := PinnedTLSConfig(pinned.pool(t))
	leaf := impostor.leaf(t, "key-01JWORKLOAD", ServiceServerName)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: cfg.RootCAs, DNSName: cfg.ServerName}); err == nil {
		t.Fatal("a leaf signed by a CA that is not the pinned one must be rejected")
	}
}

func TestCAPoolFromFileRejectsANonPEMFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CAPoolFromFile(path); err == nil {
		t.Fatal("a file with no PEM certificates must fail closed rather than yield an empty trust pool")
	}
}

func TestCAPoolFromFileLoadsAPinnedRoot(t *testing.T) {
	ca := newTestCA(t)
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, ca.certPEM, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pool, err := CAPoolFromFile(path)
	if err != nil {
		t.Fatalf("CAPoolFromFile: %v", err)
	}
	leaf := ca.leaf(t, "control-api", ServiceServerName)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: ServiceServerName}); err != nil {
		t.Fatalf("the loaded pool must verify that CA's own leaves: %v", err)
	}
}

func TestCAPoolFromFileReportsAMissingFile(t *testing.T) {
	if _, err := CAPoolFromFile(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Fatal("a missing pinned CA root must fail closed")
	}
}
