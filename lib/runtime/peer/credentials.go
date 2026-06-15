// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Per-peer TLS-mode handling shared by every rimsky→peer dial site.
//
// rimsky.yml peer entries (claim_producers / executors / publishers)
// carry a `tls:` key validated at config-parse time to the closed enum
// `off | required` (empty → off). Every gRPC dial out of rimsky maps
// that mode through TransportCredentials so the key can never be
// accepted-but-ignored: `required` dials with verified TLS (system
// roots by default), `off` stays plaintext.
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

// @constraint: TLS modes accepted on a peer entry's `tls:` key.
// Validated at config-parse time (lib/control/config); dial sites may
// assume the mode is one of these two values or empty (treated as off).
const (
	TLSModeOff      = "off"
	TLSModeRequired = "required"
)

var (
	tlsRootCAsMu sync.RWMutex
	// tlsRootCAs overrides the root pool used to verify peers under
	// TLSModeRequired. nil → system roots (the production default).
	tlsRootCAs *x509.CertPool
)

// SetTLSRootCAsForTesting overrides the certificate pool used to verify
// peer certificates under TLSModeRequired. Test seam only: it lets an
// integration test stand up a self-signed TLS peer and have the REAL
// dial path verify it. Pass nil to restore the production default
// (system roots). Safe for concurrent use.
func SetTLSRootCAsForTesting(pool *x509.CertPool) {
	tlsRootCAsMu.Lock()
	defer tlsRootCAsMu.Unlock()
	tlsRootCAs = pool
}

// TransportCredentials maps a validated peer `tls:` mode to gRPC
// transport credentials. TLSModeRequired → verified TLS against system
// roots (or the test-injected pool); anything else (TLSModeOff or the
// empty default) → plaintext. Config-parse validation guarantees no
// third value reaches a dial site, so the non-required arm never
// silently downgrades a mode an operator asked for.
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

// TLSClientConfig returns the *tls.Config rimsky uses to verify peers
// under TLSModeRequired on non-gRPC (HTTP-bridge) dials: system roots
// by default, or the test-injected pool from SetTLSRootCAsForTesting.
// Same verification posture as TransportCredentials — the two transports
// must not diverge on what "required" means.
func TLSClientConfig() *tls.Config {
	tlsRootCAsMu.RLock()
	defer tlsRootCAsMu.RUnlock()
	return &tls.Config{
		RootCAs:    tlsRootCAs,
		MinVersion: tls.VersionTLS12,
	}
}

// TLSModeUnaryInterceptor annotates RPC errors with the peer name and
// TLS mode when the peer is configured `tls: required`, so a handshake
// failure against (e.g.) a plaintext peer surfaces loudly and names
// both the peer and the mode the operator configured. No-op wrapper
// under any other mode. fmt.Errorf's %w keeps gRPC status extraction
// (status.FromError unwraps) intact for callers that branch on codes.
//
// The prefix is scoped to TRANSPORT-class failures
// (isTransportClassError): a producer-authored application error
// (InvalidArgument, NotFound, a classed Unavailable from a reached
// server, …) comes from a peer the TLS layer connected to fine —
// prefixing it with "(tls: required)" would misattribute the
// producer's own rejection to the transport.
func TLSModeUnaryInterceptor(name, mode string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil && mode == TLSModeRequired && isTransportClassError(err) {
			return fmt.Errorf("peer %q (tls: required): %w", name, err)
		}
		return err
	}
}

// TLSModeStreamInterceptor is the stream-RPC twin of
// TLSModeUnaryInterceptor: stream-establishment errors under
// `tls: required` carry the peer name and mode (same transport-class
// scoping).
func TLSModeStreamInterceptor(name, mode string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		s, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil && mode == TLSModeRequired && isTransportClassError(err) {
			return nil, fmt.Errorf("peer %q (tls: required): %w", name, err)
		}
		return s, err
	}
}

// isTransportClassError reports whether err looks like a
// connection/handshake-level failure rather than an application error
// a reached server returned. gRPC surfaces both TLS-vs-plaintext
// mismatches and unreachable peers as codes.Unavailable produced by
// the client transport (no server-set status); a non-status error is
// transport-class by construction. Known tradeoff: a server that
// itself returns a bare Unavailable is indistinguishable from the
// transport and gets the prefix — over-attribution on that one code is
// accepted to keep the TLS-misconfiguration diagnosis loud.
func isTransportClassError(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return true
	}
	return st.Code() == codes.Unavailable
}
