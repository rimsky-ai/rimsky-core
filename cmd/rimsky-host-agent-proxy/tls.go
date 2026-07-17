// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent-proxy
package main

import (
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc/credentials"
)

func proxyServerCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if cfg.TLSCertPath == "" && cfg.TLSKeyPath == "" {
		return nil, nil
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" {
		return nil, fmt.Errorf("both RIMSKY_PROXY_TLS_CERT and RIMSKY_PROXY_TLS_KEY are required to enable agent-facing TLS")
	}
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load proxy TLS keypair: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.NoClientCert,
	}), nil
}
