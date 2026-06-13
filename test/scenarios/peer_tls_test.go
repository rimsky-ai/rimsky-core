// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-peer-tls-enforced executable proof.
//
// As an operator who configures `tls: required` on a peer service, I
// get a TLS-verified connection to that peer — and a loud failure if
// the peer cannot present credentials — so that the config key means
// what it says (TD-peer-tls-enforcement + TD-tls-mode-validation).
//
//  1. A stub gRPC claim-producer peer serves with a self-signed
//     localhost certificate. The REAL dial path (peer.Dial — the same
//     function dialRemoteStores runs at startup) under `tls: required`
//     establishes a verified TLS channel and exchanges the
//     Capabilities request end-to-end — negating the falsifier's
//     "connection observed on the wire in plaintext" clause: the
//     server side holds TLS-only credentials, so a plaintext dial
//     cannot have produced the response.
//  2. Companion: the same stub served PLAINTEXT, dialed under
//     `tls: required`, fails loudly with an error naming the peer and
//     the mode — negating the falsifier's "key accepted and silently
//     ignored" clause.
//  3. Control: the plaintext stub under `tls: off` (today's default)
//     still works, pinning that off stays plaintext.
//
// The test injects its self-signed CA into the credential helper's
// root pool via the explicit test seam
// (peer.SetTLSRootCAsForTesting); the production default remains
// system roots.

package scenarios

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// tlsProofProducer is a minimal genv1.ClaimProducerServer: just enough
// Capabilities surface for peer.Dial's startup handshake to complete
// (Dial rejects an empty write_semantics_allowed envelope).
type tlsProofProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (tlsProofProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:             []string{"claim_producer"},
	}, nil
}

// selfSignedLocalhostCert mints an ephemeral self-signed certificate
// valid for 127.0.0.1 and returns the server's TLS keypair plus a cert
// pool holding the certificate (the pool the dialing side verifies
// against).
func selfSignedLocalhostCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "peer-tls-proof"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// serveStubProducer starts the stub claim-producer on a loopback
// listener (TLS when cert is non-nil, plaintext otherwise) and returns
// its address. Stopped via t.Cleanup.
func serveStubProducer(t *testing.T, cert *tls.Certificate) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var opts []grpc.ServerOption
	if cert != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{*cert},
			MinVersion:   tls.VersionTLS12,
		})))
	}
	srv := grpc.NewServer(opts...)
	genv1.RegisterClaimProducerServer(srv, tlsProofProducer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// TestPeerTLS_Required_VerifiedTLSEndToEnd dials a TLS-serving stub
// peer under `tls: required` through the real peer.Dial path and
// exchanges the Capabilities request.
func TestPeerTLS_Required_VerifiedTLSEndToEnd(t *testing.T) {
	cert, pool := selfSignedLocalhostCert(t)
	addr := serveStubProducer(t, &cert)

	peer.SetTLSRootCAsForTesting(pool)
	t.Cleanup(func() { peer.SetTLSRootCAsForTesting(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := peer.Dial(ctx, "tls-producer", "grpc://"+addr, peer.TLSModeRequired)
	if err != nil {
		t.Fatalf("Dial under tls: required against TLS peer failed: %v", err)
	}
	defer client.Close()

	// Exchange a request over the established channel and assert real
	// data crossed the wire: the stub's advertised envelope.
	caps, err := client.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities over TLS channel: %v", err)
	}
	if len(caps.WriteSemanticsAllowed) != 1 {
		t.Fatalf("Capabilities envelope = %v, want the stub's single sync entry", caps.WriteSemanticsAllowed)
	}
}

// TestPeerTLS_Required_PlaintextPeer_LoudFailure dials a PLAINTEXT
// stub under `tls: required` and asserts the loud failure names the
// peer and the mode.
func TestPeerTLS_Required_PlaintextPeer_LoudFailure(t *testing.T) {
	addr := serveStubProducer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := peer.Dial(ctx, "plaintext-producer", "grpc://"+addr, peer.TLSModeRequired)
	if err == nil {
		client.Close()
		t.Fatalf("Dial under tls: required against plaintext peer succeeded; want loud failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"plaintext-producer"`) {
		t.Fatalf("failure %q does not name the peer", msg)
	}
	if !strings.Contains(msg, "tls: required") {
		t.Fatalf("failure %q does not name the mode", msg)
	}
}

// TestPeerTLS_Off_StaysPlaintext pins the default: `tls: off` against
// a plaintext peer is today's working behavior.
func TestPeerTLS_Off_StaysPlaintext(t *testing.T) {
	addr := serveStubProducer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := peer.Dial(ctx, "plaintext-producer", "grpc://"+addr, peer.TLSModeOff)
	if err != nil {
		t.Fatalf("Dial under tls: off against plaintext peer failed: %v", err)
	}
	client.Close()
}
