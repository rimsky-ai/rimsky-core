// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peer

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type dialTLSTestClaimProducerServer struct {
	genv1.UnimplementedClaimProducerServer
}

func (dialTLSTestClaimProducerServer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func startMTLSClaimProducerServer(t *testing.T, ca *pki.CA, keyID string) string {
	t.Helper()
	issued, err := ca.IssueLeaf(keyID, time.Now().Add(-time.Hour), 48*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf(%s): %v", keyID, err)
	}
	pair, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pair},
		ClientCAs:    ca.CertPool(),
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	})))
	genv1.RegisterClaimProducerServer(srv, dialTLSTestClaimProducerServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%s): %v", lis.Addr(), err)
	}
	return "localhost:" + port
}

func TestDial_TLSModeRequired_UsesTransportCredentials(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	SetTLSRootCAsForTesting(ca.CertPool())
	t.Cleanup(func() { SetTLSRootCAsForTesting(nil) })
	SetClientIdentity(holderFor(t, ca, clientKeyID))
	t.Cleanup(func() { SetClientIdentity(nil) })

	endpoint := startMTLSClaimProducerServer(t, ca, "localhost")

	client, err := Dial(context.Background(), "producer-mtls", endpoint, TLSModeRequired)
	if err != nil {
		t.Fatalf("Dial(required) against an mTLS server with a valid client identity must succeed: %v", err)
	}
	t.Cleanup(client.Close)
}

func TestDial_TLSModeOff_DoesNotSatisfyMTLSServer(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	SetTLSRootCAsForTesting(ca.CertPool())
	t.Cleanup(func() { SetTLSRootCAsForTesting(nil) })
	SetClientIdentity(holderFor(t, ca, clientKeyID))
	t.Cleanup(func() { SetClientIdentity(nil) })

	endpoint := startMTLSClaimProducerServer(t, ca, "localhost")

	_, err = Dial(context.Background(), "producer-plaintext", endpoint, TLSModeOff)
	if err == nil {
		t.Fatalf("Dial(off) dialed plaintext against a server that requires mTLS; " +
			"a client that ignores tlsMode and always dials insecurely would wrongly succeed here")
	}
}
