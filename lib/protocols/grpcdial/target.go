// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package grpcdial

import (
	"crypto/tls"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const TLSModeRequired = "required"

var acceptedSchemes = []string{"grpc://", "http://", "https://"}

func Target(endpoint string) string {
	for _, prefix := range acceptedSchemes {
		if strings.HasPrefix(endpoint, prefix) {
			return endpoint[len(prefix):]
		}
	}
	return endpoint
}

func TransportCredentials(tlsMode string) credentials.TransportCredentials {
	if tlsMode == TLSModeRequired {
		return credentials.NewTLS(tlsClientConfig())
	}
	return insecure.NewCredentials()
}

func tlsClientConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}
