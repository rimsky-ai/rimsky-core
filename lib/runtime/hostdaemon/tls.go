// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
package hostdaemon

import (
	"crypto/tls"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

// @decision: host-daemon-proxy-tls
// @concept: service-auth
func daemonTransportCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if cfg.Insecure {
		return insecure.NewCredentials(), nil
	}
	if cfg.TLSCAPath == "" {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}), nil
	}
	pool, err := enroll.CAPoolFromFile(cfg.TLSCAPath)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(enroll.PinnedTLSConfig(pool)), nil
}
