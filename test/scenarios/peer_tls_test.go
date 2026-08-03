// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type tlsProofProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (tlsProofProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		Protocols:             []string{"claim_producer"},
	}, nil
}

// @decision: peer-auth-mtls
func selfSignedPeerCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
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
		DNSNames:              []string{"localhost", enroll.PeerServerName},
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

func TestPeerTLS_Required_VerifiedTLSEndToEnd(t *testing.T) {
	cert, pool := selfSignedPeerCert(t)
	addr := serveStubProducer(t, &cert)

	peer.SetTLSRootCAsForTesting(pool)
	t.Cleanup(func() { peer.SetTLSRootCAsForTesting(nil) })

	ctx := context.Background()
	client, err := peer.Dial(ctx, "tls-producer", "grpc://"+addr, peer.TLSModeRequired)
	if err != nil {
		t.Fatalf("Dial under tls: required against TLS peer failed: %v", err)
	}
	defer client.Close()

	caps, err := client.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities over TLS channel: %v", err)
	}
	if len(caps.WriteSemanticsAllowed) != 1 {
		t.Fatalf("Capabilities envelope = %v, want the stub's single sync entry", caps.WriteSemanticsAllowed)
	}
}

func TestPeerTLS_Required_PlaintextPeer_LoudFailure(t *testing.T) {
	addr := serveStubProducer(t, nil)

	client, err := peer.Dial(context.Background(), "plaintext-producer", "grpc://"+addr, peer.TLSModeRequired)
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

func TestPeerTLS_Off_StaysPlaintext(t *testing.T) {
	addr := serveStubProducer(t, nil)

	client, err := peer.Dial(context.Background(), "plaintext-producer", "grpc://"+addr, peer.TLSModeOff)
	if err != nil {
		t.Fatalf("Dial under tls: off against plaintext peer failed: %v", err)
	}
	client.Close()
}
