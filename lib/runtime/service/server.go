// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"crypto/tls"
	"crypto/x509"
)

func TLSServerConfig(serviceAuth string, serverIdentity *IdentityHolder, clientCAs *x509.CertPool) *tls.Config {
	if serviceAuth != ServiceAuthMTLS {
		return nil
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: serverIdentity.GetCertificate,
		ClientCAs:      clientCAs,
		ClientAuth:     tls.RequireAndVerifyClientCert,
	}
}

// @concept: service-auth
// @decision: service-auth-mtls
func TLSControlAPIServerConfig(serviceAuth string, serverIdentity *IdentityHolder, clientCAs *x509.CertPool) *tls.Config {
	cfg := TLSServerConfig(serviceAuth, serverIdentity, clientCAs)
	if cfg == nil {
		return nil
	}
	cfg.ClientAuth = tls.VerifyClientCertIfGiven
	return cfg
}
