// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy
package main

import (
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
)

// @concept: peer-auth
func proxyServerCredentials(cfg Config, identity *peerauth.Identity) (credentials.TransportCredentials, string, error) {
	if cfg.TLSCertPath == "" && cfg.TLSKeyPath == "" {
		if tlsCfg := identity.ServerOnlyTLSConfig(); tlsCfg != nil {
			return credentials.NewTLS(tlsCfg), "enrolled deployment-CA leaf", nil
		}
		return nil, "", nil
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" {
		return nil, "", fmt.Errorf("both RIMSKY_PROXY_TLS_CERT and RIMSKY_PROXY_TLS_KEY are required to enable agent-facing TLS")
	}
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
	if err != nil {
		return nil, "", fmt.Errorf("load proxy TLS keypair: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.NoClientCert,
	}), "operator-mounted keypair " + cfg.TLSCertPath, nil
}
