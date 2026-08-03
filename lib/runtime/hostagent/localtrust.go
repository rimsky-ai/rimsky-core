// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent
package hostagent

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

const localTrustServerName = "localhost"

// @decision: host-agent-proxy-tls
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
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{lt.agentCert},
		NextProtos:   []string{"h2"},
		RootCAs:      lt.caPool,
		ServerName:   enroll.PeerServerName,
	}
}
