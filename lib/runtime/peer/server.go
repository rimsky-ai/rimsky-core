// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
