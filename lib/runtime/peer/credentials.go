// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

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
)

const (
	TLSModeOff      = "off"
	TLSModeRequired = "required"
)

var (
	tlsRootCAsMu sync.RWMutex
	tlsRootCAs   *x509.CertPool
)

func SetTLSRootCAsForTesting(pool *x509.CertPool) {
	tlsRootCAsMu.Lock()
	defer tlsRootCAsMu.Unlock()
	tlsRootCAs = pool
}

func TransportCredentials(mode string) credentials.TransportCredentials {
	if mode != TLSModeRequired {
		return insecure.NewCredentials()
	}
	tlsRootCAsMu.RLock()
	defer tlsRootCAsMu.RUnlock()
	return credentials.NewTLS(&tls.Config{
		RootCAs:    tlsRootCAs,
		MinVersion: tls.VersionTLS12,
	})
}

func TLSClientConfig() *tls.Config {
	tlsRootCAsMu.RLock()
	defer tlsRootCAsMu.RUnlock()
	return &tls.Config{
		RootCAs:    tlsRootCAs,
		MinVersion: tls.VersionTLS12,
	}
}

func TLSModeUnaryInterceptor(name, mode string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil && mode == TLSModeRequired && isTransportClassError(err) {
			return fmt.Errorf("peer %q (tls: required): %w", name, err)
		}
		return err
	}
}

func TLSModeStreamInterceptor(name, mode string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		s, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil && mode == TLSModeRequired && isTransportClassError(err) {
			return nil, fmt.Errorf("peer %q (tls: required): %w", name, err)
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
