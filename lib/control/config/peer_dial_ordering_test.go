// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type syncCapabilitiesProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (syncCapabilitiesProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func startMTLSClaimProducer(t *testing.T, ca *pki.CA) string {
	t.Helper()
	issued, err := ca.IssueLeaf("localhost", time.Now().Add(-time.Hour), 48*time.Hour)
	if err != nil {
		t.Fatalf("IssueLeaf(localhost): %v", err)
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
	genv1.RegisterClaimProducerServer(srv, syncCapabilitiesProducer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%s): %v", lis.Addr(), err)
	}
	return "localhost:" + port
}

func TestStartSupervisor_MTLSWiresIdentityBeforeOutboundDial(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(key))
	t.Cleanup(func() { peer.SetClientIdentity(nil); peer.SetTLSRootCAs(nil) })

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rimsky.db")
	db, err := persistence.Open(ctx, persistence.Config{Driver: "sqlite", SQLite: &persistence.SQLiteConfig{Path: dbPath}})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ca, err := ensureDeploymentCA(ctx, db.Tables(), shared.SystemClock{})
	if err != nil {
		t.Fatalf("ensureDeploymentCA: %v", err)
	}

	endpoint := startMTLSClaimProducer(t, ca)

	handle, err := StartSupervisor(SupervisorConfig{
		SupervisorID: "supervisor-ordering",
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		Resolver:     executor.NewStaticResolver(nil),
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		PeerAuth:     peer.PeerAuthMTLS,
		ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
			"items-store": {
				Endpoint:     endpoint,
				TLS:          peer.TLSModeRequired,
				Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("StartSupervisor under mtls must dial the remote claim producer with a client cert already loaded (identity wired pre-dial); got: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })
}
