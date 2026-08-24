// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

const (
	TLSModeOff      = "off"
	TLSModeRequired = "required"
)

var (
	tlsRootCAsMu sync.RWMutex
	tlsRootCAs   *x509.CertPool

	clientIdentityMu sync.RWMutex
	clientIdentity   *IdentityHolder
)

func SetTLSRootCAs(pool *x509.CertPool) {
	tlsRootCAsMu.Lock()
	defer tlsRootCAsMu.Unlock()
	tlsRootCAs = pool
}

func SetClientIdentity(h *IdentityHolder) {
	clientIdentityMu.Lock()
	defer clientIdentityMu.Unlock()
	clientIdentity = h
}

type ClientCredentials struct {
	RootCAs  *x509.CertPool
	Identity *IdentityHolder
}

func ProcessClientCredentials() ClientCredentials {
	tlsRootCAsMu.RLock()
	pool := tlsRootCAs
	tlsRootCAsMu.RUnlock()
	clientIdentityMu.RLock()
	h := clientIdentity
	clientIdentityMu.RUnlock()
	return ClientCredentials{RootCAs: pool, Identity: h}
}

// @decision: service-tls-enforcement
func TransportCredentials(mode string) credentials.TransportCredentials {
	return TransportCredentialsFor(mode, ProcessClientCredentials())
}

func TransportCredentialsFor(mode string, creds ClientCredentials) credentials.TransportCredentials {
	if mode != TLSModeRequired {
		return insecure.NewCredentials()
	}
	return credentials.NewTLS(TLSClientConfigFor(creds))
}

func TLSClientConfig() *tls.Config {
	return TLSClientConfigFor(ProcessClientCredentials())
}

// @concept: service-auth
// @decision: service-auth-mtls
func TLSClientConfigFor(creds ClientCredentials) *tls.Config {
	cfg := &tls.Config{
		RootCAs:    creds.RootCAs,
		MinVersion: tls.VersionTLS12,
	}
	if creds.RootCAs != nil {
		cfg.ServerName = enroll.ServiceServerName
	}
	if creds.Identity != nil && creds.Identity.HasIdentity() {
		cfg.GetClientCertificate = creds.Identity.GetClientCertificate
	}
	return cfg
}

func TLSModeUnaryInterceptor(name, mode string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil && mode == TLSModeRequired && isTransportClassError(err) {
			return fmt.Errorf("service %q (tls: required): %w", name, err)
		}
		return err
	}
}

func TLSModeStreamInterceptor(name, mode string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		s, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil && mode == TLSModeRequired && isTransportClassError(err) {
			return nil, fmt.Errorf("service %q (tls: required): %w", name, err)
		}
		return s, err
	}
}

func isTransportClassError(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return true
	}
	return st.Code() == codes.Unavailable
}
