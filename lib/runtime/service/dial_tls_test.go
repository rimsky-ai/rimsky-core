// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

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
	return startMTLSClaimProducerServerTrusting(t, ca, ca, keyID)
}

func startMTLSClaimProducerServerTrusting(t *testing.T, serverIssuer, clientIssuer *pki.CA, keyID string) string {
	t.Helper()
	ca := serverIssuer
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
		ClientCAs:    clientIssuer.CertPool(),
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
	creds := ClientCredentials{RootCAs: ca.CertPool(), Identity: holderFor(t, ca, clientKeyID)}

	endpoint := startMTLSClaimProducerServer(t, ca, "localhost")

	client, err := DialWith(context.Background(), "producer-mtls", endpoint, TLSModeRequired, creds)
	if err != nil {
		t.Fatalf("Dial(required) against an mTLS server with a valid client identity must succeed: %v", err)
	}
	t.Cleanup(client.Close)
}

// @concept: service-auth
// @story: service-auth-mtls-mutual
func TestDial_TLSModeRequired_AcceptsAServiceWhoseLeafNamesItsPrincipalNotItsHost(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	creds := ClientCredentials{RootCAs: ca.CertPool(), Identity: holderFor(t, ca, clientKeyID)}

	endpoint := startMTLSClaimProducerServer(t, ca, "key-01JENROLLEDSERVICE")

	client, err := DialWith(context.Background(), "producer-mtls", endpoint, TLSModeRequired, creds)
	if err != nil {
		t.Fatalf("enrollment issues a leaf whose SAN is the service's api-key principal, never the hostname "+
			"the config points at, so binding the dial to the endpoint hostname would break every "+
			"forward leg under service_auth: mtls: %v", err)
	}
	t.Cleanup(client.Close)
}

func TestDial_TLSModeRequired_RejectsAServiceCertFromAnotherCA(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	impostor, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA impostor: %v", err)
	}
	creds := ClientCredentials{RootCAs: ca.CertPool(), Identity: holderFor(t, ca, clientKeyID)}

	endpoint := startMTLSClaimProducerServerTrusting(t, impostor, ca, "key-01JIMPOSTOR")

	if _, err := DialWith(context.Background(), "producer-impostor", endpoint, TLSModeRequired, creds); err == nil {
		t.Fatal("dropping hostname verification must not drop issuer verification: a leaf signed by a CA " +
			"that is not this deployment's must still be refused")
	}
}

func TestDial_TLSModeOff_DoesNotSatisfyMTLSServer(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	creds := ClientCredentials{RootCAs: ca.CertPool(), Identity: holderFor(t, ca, clientKeyID)}

	endpoint := startMTLSClaimProducerServer(t, ca, "localhost")

	_, err = DialWith(context.Background(), "producer-plaintext", endpoint, TLSModeOff, creds)
	if err == nil {
		t.Fatalf("Dial(off) dialed plaintext against a server that requires mTLS; " +
			"a client that ignores tlsMode and always dials insecurely would wrongly succeed here")
	}
}
