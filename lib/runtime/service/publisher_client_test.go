// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type fakePublisherServer struct {
	genv1.UnimplementedPublisherServer
}

func (fakePublisherServer) Subscribe(context.Context, *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	return &genv1.SubscribeResponse{}, nil
}

func startMTLSPublisherServer(t *testing.T, ca *pki.CA, keyID string) string {
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
	genv1.RegisterPublisherServer(srv, fakePublisherServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%s): %v", lis.Addr(), err)
	}
	return "localhost:" + port
}

func TestDialPublisher_TLSModeRequired_UsesTransportCredentials(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	creds := ClientCredentials{RootCAs: ca.CertPool(), Identity: holderFor(t, ca, clientKeyID)}

	endpoint := startMTLSPublisherServer(t, ca, "localhost")

	client, err := DialPublisherWith(context.Background(), "pub-mtls", endpoint, TLSModeRequired, creds)
	if err != nil {
		t.Fatalf("DialPublisher(required): %v", err)
	}
	t.Cleanup(client.Close)

	err = client.Subscribe(context.Background(), sampleSubscribeRequest())
	if err != nil {
		t.Fatalf("DialPublisher(required) against an mTLS server with a valid client identity must succeed: %v", err)
	}
}

func TestDialPublisher_TLSModeOff_DoesNotSatisfyMTLSServer(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	creds := ClientCredentials{RootCAs: ca.CertPool(), Identity: holderFor(t, ca, clientKeyID)}

	endpoint := startMTLSPublisherServer(t, ca, "localhost")

	client, err := DialPublisherWith(context.Background(), "pub-plaintext", endpoint, TLSModeOff, creds)
	if err != nil {
		t.Fatalf("DialPublisher(off): %v", err)
	}
	t.Cleanup(client.Close)

	err = client.Subscribe(context.Background(), sampleSubscribeRequest())
	if err == nil {
		t.Fatalf("DialPublisher(off) dialed plaintext against a server that requires mTLS; " +
			"the RPC must fail — a client that ignores tlsMode and always dials insecurely would " +
			"wrongly succeed here")
	}
}

func sampleSubscribeRequest() clientiface.SubscribeRequest {
	return clientiface.SubscribeRequest{
		PublisherSubscriptionID: shared.UUID(uuid.New()),
		InstanceID:              shared.UUID(uuid.New()),
		Kind:                    "http",
		ResolvedConfig:          []byte(`{}`),
		MessageType:             "system/invalidate",
	}
}
