// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
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
	t.Cleanup(func() { service.SetClientIdentity(nil); service.SetTLSRootCAs(nil) })

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
		ServiceAuth:  service.ServiceAuthMTLS,
		ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
			"items-store": {
				Endpoint:     endpoint,
				TLS:          service.TLSModeRequired,
				Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("StartSupervisor under mtls must dial the remote claim producer with a client cert already loaded (identity wired pre-dial); got: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })
}

func TestStartScheduler_MTLSWiresIdentityBeforeOutboundDial(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(key))
	t.Cleanup(func() { service.SetClientIdentity(nil); service.SetTLSRootCAs(nil) })

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

	handle, err := StartScheduler(SchedulerConfig{
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		TickInterval: 250 * time.Millisecond,
		SupervisorID: "scheduler-ordering",
		ServiceAuth:  service.ServiceAuthMTLS,
		ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
			"items-store": {
				Endpoint:     endpoint,
				TLS:          service.TLSModeRequired,
				Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("StartScheduler under mtls must dial the remote claim producer with a client cert already loaded (identity wired pre-dial); got: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })
}

func TestStartControlAPI_MTLSWiresIdentityBeforeOutboundDial(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(key))
	t.Cleanup(func() { service.SetClientIdentity(nil); service.SetTLSRootCAs(nil) })

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

	handle, err := StartControlAPI(ControlAPIConfig{
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		Host:         "127.0.0.1",
		Port:         0,
		ControlAPIID: "control-api-ordering",
		ServiceAuth:  service.ServiceAuthMTLS,
		ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
			"items-store": {
				Endpoint:     endpoint,
				TLS:          service.TLSModeRequired,
				Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("StartControlAPI under mtls must dial the remote claim producer with a client cert already loaded (identity wired pre-dial); got: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })
}

// @concept: service-auth
func TestServiceAuthMTLS_SplitRoleWiring_EachRoleInstallsOwnIdentity(t *testing.T) {
	roles := []struct {
		name  string
		start func(t *testing.T, endpoint string, db persistence.Database) (interface{ Shutdown(context.Context) error }, error)
	}{
		{
			name: "supervisor",
			start: func(t *testing.T, endpoint string, db persistence.Database) (interface{ Shutdown(context.Context) error }, error) {
				return StartSupervisor(SupervisorConfig{
					SupervisorID: "split-role-supervisor",
					Driver:       db,
					Clock:        shared.SystemClock{},
					Logger:       shared.SilentLogger{},
					Resolver:     executor.NewStaticResolver(nil),
					CallbackHost: "127.0.0.1",
					CallbackPort: 0,
					ServiceAuth:  service.ServiceAuthMTLS,
					ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
						"items-store": {
							Endpoint:     endpoint,
							TLS:          service.TLSModeRequired,
							Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
						},
					}},
				})
			},
		},
		{
			name: "scheduler",
			start: func(t *testing.T, endpoint string, db persistence.Database) (interface{ Shutdown(context.Context) error }, error) {
				return StartScheduler(SchedulerConfig{
					Driver:       db,
					Clock:        shared.SystemClock{},
					Logger:       shared.SilentLogger{},
					TickInterval: 250 * time.Millisecond,
					SupervisorID: "split-role-scheduler",
					ServiceAuth:  service.ServiceAuthMTLS,
					ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
						"items-store": {
							Endpoint:     endpoint,
							TLS:          service.TLSModeRequired,
							Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
						},
					}},
				})
			},
		},
		{
			name: "control-api",
			start: func(t *testing.T, endpoint string, db persistence.Database) (interface{ Shutdown(context.Context) error }, error) {
				return StartControlAPI(ControlAPIConfig{
					Driver:       db,
					Clock:        shared.SystemClock{},
					Logger:       shared.SilentLogger{},
					Host:         "127.0.0.1",
					Port:         0,
					ControlAPIID: "split-role-control-api",
					ServiceAuth:  service.ServiceAuthMTLS,
					ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
						"items-store": {
							Endpoint:     endpoint,
							TLS:          service.TLSModeRequired,
							Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
						},
					}},
				})
			},
		},
	}

	for _, role := range roles {
		role := role
		t.Run(role.name, func(t *testing.T) {
			key := make([]byte, 32)
			for i := range key {
				key[i] = byte(i + 7)
			}
			t.Setenv(pki.EnvCAEncryptionKey, base64.StdEncoding.EncodeToString(key))

			service.SetClientIdentity(nil)
			service.SetTLSRootCAs(nil)
			t.Cleanup(func() {
				service.SetClientIdentity(nil)
				service.SetTLSRootCAs(nil)
			})

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

			handle, err := role.start(t, endpoint, db)
			if err != nil {
				t.Fatalf("role %s under mtls must install its own outbound identity in isolation — split-role wiring means no role freeloads on a sibling's global set-up. Start failed: %v", role.name, err)
			}
			t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })
		})
	}
}

// @concept: service-address-book
func TestStartSupervisor_DoesNotBootDialConfiguredProducers(t *testing.T) {
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

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	unreachable := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatalf("Close listener to free the port for a refused connection: %v", err)
	}

	handle, err := StartSupervisor(SupervisorConfig{
		SupervisorID: "supervisor-unreachable-service",
		Driver:       db,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		Resolver:     executor.NewStaticResolver(nil),
		CallbackHost: "127.0.0.1",
		CallbackPort: 0,
		ServiceAuth:  service.ServiceAuthNone,
		ClaimProducers: RemoteClaimProducersConfig{ClaimProducers: map[string]ClaimProducerEntry{
			"items-store": {
				Endpoint:     unreachable,
				TLS:          service.TLSModeOff,
				Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("StartSupervisor failed on a config naming an unreachable producer at %q — "+
			"supervisors resolve store names read-through against the service address book at dispatch "+
			"time and must not boot-dial per-process producer config: %v", unreachable, err)
	}
	if err := handle.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
