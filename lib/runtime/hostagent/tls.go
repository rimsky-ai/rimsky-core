// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent
package hostagent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var errTLSCARequired = errors.New("hostagent: RIMSKY_AGENT_TLS is on but no CA root was provided (--tls-ca / RIMSKY_AGENT_TLS_CA)")

func agentTransportCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if !cfg.TLSEnabled {
		return insecure.NewCredentials(), nil
	}
	pool, err := loadPinnedCAPool(cfg.TLSCAPath)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}), nil
}

func loadPinnedCAPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, errTLSCARequired
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hostagent: read TLS CA root %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("hostagent: TLS CA root %q contains no PEM certificates", path)
	}
	return pool, nil
}
