// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

const localTrustServerName = "localhost"

type localTrust struct {
	ca        *pki.CA
	agentCert tls.Certificate
	caPool    *x509.CertPool
}

func newLocalTrust(now time.Time) (*localTrust, error) {
	ca, err := pki.GenerateCA(now)
	if err != nil {
		return nil, fmt.Errorf("hostagent: generate local CA: %w", err)
	}
	issued, err := ca.IssueLeaf(localTrustServerName, now, pki.LeafTTL)
	if err != nil {
		return nil, fmt.Errorf("hostagent: issue agent leaf: %w", err)
	}
	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("hostagent: load agent leaf: %w", err)
	}
	return &localTrust{ca: ca, agentCert: cert, caPool: ca.CertPool()}, nil
}

func (lt *localTrust) issueChildLeaf(principal string, now time.Time) (enroll.Response, error) {
	issued, err := lt.ca.IssueLeaf(principal, now, pki.LeafTTL)
	if err != nil {
		return enroll.Response{}, err
	}
	return enroll.Response{
		CertPEM:   string(issued.CertPEM),
		KeyPEM:    string(issued.KeyPEM),
		CARootPEM: string(lt.ca.CertPEM()),
		NotAfter:  issued.NotAfter,
	}, nil
}

func (lt *localTrust) callbackServerTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{lt.agentCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    lt.caPool,
	}
}

func (lt *localTrust) dialChildTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:            tls.VersionTLS12,
		Certificates:          []tls.Certificate{lt.agentCert},
		NextProtos:            []string{"h2"},
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: verifyChildAgainstPool(lt.caPool),
	}
}

func verifyChildAgainstPool(pool *x509.CertPool) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("hostagent: child presented no certificate")
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("hostagent: parse child leaf: %w", err)
		}
		opts := x509.VerifyOptions{Roots: pool, Intermediates: x509.NewCertPool()}
		for _, der := range rawCerts[1:] {
			inter, interErr := x509.ParseCertificate(der)
			if interErr != nil {
				return fmt.Errorf("hostagent: parse child intermediate: %w", interErr)
			}
			opts.Intermediates.AddCert(inter)
		}
		if _, err := leaf.Verify(opts); err != nil {
			return fmt.Errorf("hostagent: child cert not issued by local CA: %w", err)
		}
		return nil
	}
}
