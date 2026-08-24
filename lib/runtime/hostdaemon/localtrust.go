// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
package hostdaemon

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

const localTrustServerName = "localhost"

// @decision: host-daemon-proxy-tls
type localTrust struct {
	ca         *pki.CA
	daemonCert tls.Certificate
	caPool     *x509.CertPool
}

func newLocalTrust(now time.Time) (*localTrust, error) {
	ca, err := pki.GenerateCA(now)
	if err != nil {
		return nil, fmt.Errorf("hostdaemon: generate local CA: %w", err)
	}
	issued, err := ca.IssueLeaf(localTrustServerName, now, pki.LeafTTL)
	if err != nil {
		return nil, fmt.Errorf("hostdaemon: issue daemon leaf: %w", err)
	}
	cert, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("hostdaemon: load daemon leaf: %w", err)
	}
	return &localTrust{ca: ca, daemonCert: cert, caPool: ca.CertPool()}, nil
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
		Certificates: []tls.Certificate{lt.daemonCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    lt.caPool,
	}
}

func (lt *localTrust) dialChildTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{lt.daemonCert},
		NextProtos:   []string{"h2"},
		RootCAs:      lt.caPool,
		ServerName:   enroll.ServiceServerName,
	}
}
