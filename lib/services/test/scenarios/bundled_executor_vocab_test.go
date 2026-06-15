// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Smoke tests for the bundled-executor hierarchical error-class
// vocabulary introduced by spec
// .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
// Pass 6. Each test asserts at least one emission carries a hierarchical
// `<executor>/<leaf>` error class — the convention every bundled
// executor follows under `concept:signal`'s hierarchical class rule.
//
// The three http-node / verifier-* sub-tests are static checks on the
// per-executor `Declared()` vocabulary; the postgres-stores sub-test
// boots the lib/services postgres store binary in-process against a
// real Postgres testcontainer (via test/harness.StartFreshPostgres) and
// drives an Execute with missing attributes to surface the live
// `pg/attribute_invalid` class on the wire.
package scenarios

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	httpnodeerrclasses "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node/errorclasses"
	verifierhttperrclasses "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http/errorclasses"
	verifiershapeerrclasses "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/errorclasses"
	pgstoreserver "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/server"
	pgstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestHttpNode_EmitsHierarchicalErrorClasses asserts the http-node
// executor's declared error vocabulary follows the `http/<leaf>`
// hierarchical convention per `concept:signal`. Importing the canonical
// `pkg:executors/http-node/errorclasses` Declared() helper makes any
// drift between the executor's runtime advertisement and the contract
// a compile-or-test-time failure.
func TestHttpNode_EmitsHierarchicalErrorClasses(t *testing.T) {
	t.Parallel()
	declared := httpnodeerrclasses.Declared()
	if len(declared) == 0 {
		t.Fatal("http-node must declare at least one error class")
	}
	for _, c := range declared {
		if c == "" {
			t.Errorf("declared error class is empty string")
			continue
		}
		assertHierarchical(t, "http-node", c)
	}
}

// TestPostgresStores_EmitsHierarchicalErrorClasses boots the fused
// stores/postgres/ server (ClaimProducer + Executor on one endpoint)
// against a real Postgres testcontainer, drives an Execute against
// missing attributes, and asserts the emission carries
// `pg/attribute_invalid` (the hierarchical leaf per `concept:signal`).
func TestPostgresStores_EmitsHierarchicalErrorClasses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	st, err := pgstore.New(ctx, pgstore.Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
	})
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, pgstoreserver.NewExecutorServer(st))
	genv1.RegisterExecutorObservabilityServer(srv, pgstoreserver.NewExecutorObservabilityServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	obsClient := genv1.NewExecutorObservabilityClient(conn)
	caps, err := obsClient.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.GetDeclaredErrorClasses()) == 0 {
		t.Fatal("postgres-stores executor must declare hierarchical error classes")
	}
	for _, c := range caps.GetDeclaredErrorClasses() {
		assertHierarchical(t, "pg-executor", c)
	}

	ud, _ := structpb.NewStruct(map[string]any{})
	execClient := genv1.NewExecutorClient(conn)
	stream, err := execClient.Execute(ctx, &genv1.ExecuteRequest{Attributes: ud})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var emittedClass string
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			break
		}
		if sc := ev.GetStreamClose(); sc != nil {
			if e := sc.GetError(); e != nil {
				emittedClass = e.GetErrorClass()
				break
			}
		}
	}
	if emittedClass != "pg/attribute_invalid" {
		t.Errorf("postgres-stores executor with missing attributes: got %q want pg/attribute_invalid", emittedClass)
	}
}

// TestVerifierHttp_EmitsHierarchicalErrorClasses asserts the
// verifier-http bundled executor's declared error vocabulary follows
// the `verifier/<leaf>` hierarchical convention.
func TestVerifierHttp_EmitsHierarchicalErrorClasses(t *testing.T) {
	t.Parallel()
	declared := verifierhttperrclasses.Declared()
	if len(declared) == 0 {
		t.Fatal("verifier-http must declare at least one error class")
	}
	for _, c := range declared {
		if c == "" {
			t.Errorf("declared error class is empty string")
			continue
		}
		assertHierarchical(t, "verifier-http", c)
	}
}

// TestVerifierShapeChecks_EmitsHierarchicalErrorClasses asserts the
// verifier-shape-checks bundled executor's declared error vocabulary
// follows the `verifier/<leaf>` hierarchical convention. The
// `verifier/check_failed/*` entry exercises the `<prefix>/*` wildcard
// validator surface; per-runtime emissions populate the suffix with
// the failed check's `kind`.
func TestVerifierShapeChecks_EmitsHierarchicalErrorClasses(t *testing.T) {
	t.Parallel()
	declared := verifiershapeerrclasses.Declared()
	if len(declared) == 0 {
		t.Fatal("verifier-shape-checks must declare at least one error class")
	}
	for _, c := range declared {
		if c == "" {
			t.Errorf("declared error class is empty string")
			continue
		}
		assertHierarchical(t, "verifier-shape-checks", c)
	}
}

// assertHierarchical fails the test when c is a flat single-segment
// class string. Hierarchical convention requires either an exact path
// containing `/` or a `<prefix>/*` wildcard.
func assertHierarchical(t *testing.T, executor, c string) {
	t.Helper()
	if !containsSlash(c) {
		t.Errorf("declared %s class %q is flat; must follow the hierarchical <prefix>/<leaf> convention", executor, c)
	}
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}
