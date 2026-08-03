// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package enroll

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

const EnvControlAPICA = "RIMSKY_CONTROL_API_CA"

const PeerServerName = "peer.rimsky.internal"

func CAPoolFromPEM(source string, pemBytes []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("enroll: CA root %s contains no PEM certificates", source)
	}
	return pool, nil
}

func CAPoolFromFile(path string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("enroll: read CA root %q: %w", path, err)
	}
	return CAPoolFromPEM(fmt.Sprintf("%q", path), raw)
}

func PinnedTLSConfig(pool *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
		ServerName: PeerServerName,
	}
}
