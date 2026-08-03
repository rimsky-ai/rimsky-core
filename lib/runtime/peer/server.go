// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package peer

import (
	"crypto/tls"
	"crypto/x509"
)

func TLSServerConfig(peerAuth string, serverIdentity *IdentityHolder, clientCAs *x509.CertPool) *tls.Config {
	if peerAuth != PeerAuthMTLS {
		return nil
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: serverIdentity.GetCertificate,
		ClientCAs:      clientCAs,
		ClientAuth:     tls.RequireAndVerifyClientCert,
	}
}

// @concept: peer-auth
// @decision: peer-auth-mtls
func TLSControlAPIServerConfig(peerAuth string, serverIdentity *IdentityHolder, clientCAs *x509.CertPool) *tls.Config {
	cfg := TLSServerConfig(peerAuth, serverIdentity, clientCAs)
	if cfg == nil {
		return nil
	}
	cfg.ClientAuth = tls.VerifyClientCertIfGiven
	return cfg
}
