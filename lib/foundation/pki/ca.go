// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

const (
	LeafTTL = 24 * time.Hour

	caTTL = 10 * 365 * 24 * time.Hour

	clockSkewTolerance = time.Minute

	spiffeScheme      = "spiffe"
	spiffeTrustDomain = "rimsky"

	caCommonName = "rimsky-deployment-ca"

	pemTypeCertificate = "CERTIFICATE"
	pemTypePrivateKey  = "PRIVATE KEY"
)

var ErrPrincipalNotFound = errors.New("pki: verified peer certificate carries no spiffe://rimsky/<key-id> URI SAN")

type CA struct {
	cert    *x509.Certificate
	certPEM []byte
	key     *ecdsa.PrivateKey
}

type Issued struct {
	CertPEM   []byte
	KeyPEM    []byte
	NotBefore time.Time
	NotAfter  time.Time
}

func GenerateCA(now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pki.GenerateCA: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName},
		NotBefore:             now.Add(-clockSkewTolerance),
		NotAfter:              now.Add(caTTL),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		URIs:                  []*url.URL{trustDomainURI()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("pki.GenerateCA: create certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("pki.GenerateCA: reparse certificate: %w", err)
	}
	return &CA{
		cert:    cert,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: pemTypeCertificate, Bytes: der}),
		key:     key,
	}, nil
}

func LoadCA(certPEM, keyPKCS8DER []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != pemTypeCertificate {
		return nil, errors.New("pki.LoadCA: ca_cert_pem is not a PEM CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki.LoadCA: parse certificate: %w", err)
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(keyPKCS8DER)
	if err != nil {
		return nil, fmt.Errorf("pki.LoadCA: parse private key: %w", err)
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("pki.LoadCA: private key is not ECDSA: %T", keyAny)
	}
	return &CA{cert: cert, certPEM: certPEM, key: key}, nil
}

func (ca *CA) CertPEM() []byte {
	out := make([]byte, len(ca.certPEM))
	copy(out, ca.certPEM)
	return out
}

func (ca *CA) KeyPKCS8DER() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(ca.key)
	if err != nil {
		return nil, fmt.Errorf("pki.CA.KeyPKCS8DER: %w", err)
	}
	return der, nil
}

func (ca *CA) Certificate() *x509.Certificate { return ca.cert }

func (ca *CA) CertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

func (ca *CA) IssueLeaf(principalKeyID string, now time.Time, ttl time.Duration) (Issued, error) {
	if principalKeyID == "" {
		return Issued{}, errors.New("pki.CA.IssueLeaf: principalKeyID is required")
	}
	if ttl <= 0 {
		return Issued{}, fmt.Errorf("pki.CA.IssueLeaf: ttl must be positive, got %s", ttl)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Issued{}, fmt.Errorf("pki.CA.IssueLeaf: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return Issued{}, err
	}
	notAfter := now.Add(ttl)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: principalKeyID},
		NotBefore:    now.Add(-clockSkewTolerance),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{principalURI(principalKeyID)},
		DNSNames:     []string{principalKeyID},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &leafKey.PublicKey, ca.key)
	if err != nil {
		return Issued{}, fmt.Errorf("pki.CA.IssueLeaf: create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return Issued{}, fmt.Errorf("pki.CA.IssueLeaf: marshal leaf key: %w", err)
	}
	return Issued{
		CertPEM:   pem.EncodeToMemory(&pem.Block{Type: pemTypeCertificate, Bytes: der}),
		KeyPEM:    pem.EncodeToMemory(&pem.Block{Type: pemTypePrivateKey, Bytes: keyDER}),
		NotBefore: now,
		NotAfter:  notAfter,
	}, nil
}

func principalFromCert(cert *x509.Certificate) (string, error) {
	for _, u := range cert.URIs {
		if u.Scheme != spiffeScheme || u.Host != spiffeTrustDomain {
			continue
		}
		id := trimLeadingSlash(u.Path)
		if id != "" {
			return id, nil
		}
	}
	return "", ErrPrincipalNotFound
}

func PrincipalFromVerifiedChains(state *tls.ConnectionState) (string, error) {
	if state == nil {
		return "", ErrPrincipalNotFound
	}
	for _, chain := range state.VerifiedChains {
		if len(chain) == 0 {
			continue
		}
		if id, err := principalFromCert(chain[0]); err == nil {
			return id, nil
		}
	}
	return "", ErrPrincipalNotFound
}

func SpiffeURI(principalKeyID string) string {
	return principalURI(principalKeyID).String()
}

func principalURI(principalKeyID string) *url.URL {
	return &url.URL{Scheme: spiffeScheme, Host: spiffeTrustDomain, Path: "/" + principalKeyID}
}

func trustDomainURI() *url.URL {
	return &url.URL{Scheme: spiffeScheme, Host: spiffeTrustDomain}
}

func trimLeadingSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("pki: random serial: %w", err)
	}
	return serial, nil
}
