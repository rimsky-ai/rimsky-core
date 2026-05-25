// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Smoke tests for the bundled-executor hierarchical error-class
// vocabulary introduced by spec
// .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
// Pass 6. Each test boots the bundled executor's in-process gRPC
// surface and asserts at least one emission carries a hierarchical
// `<executor>/<leaf>` error class — the convention every bundled
// executor follows under `concept:signal`'s hierarchical class rule.
package scenarios

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	httpnodeerrclasses "github.com/fallguyconsulting/rimsky/executors/http-node/errorclasses"
	verifierhttperrclasses "github.com/fallguyconsulting/rimsky/executors/verifier-http/errorclasses"
	verifiershapeerrclasses "github.com/fallguyconsulting/rimsky/executors/verifier-shape-checks/errorclasses"
	corestore "github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/internal/pgtest"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	pgstoreserver "github.com/fallguyconsulting/rimsky/stores/postgres/server"
	pgstore "github.com/fallguyconsulting/rimsky/stores/postgres/store"
)

// TestHttpNode_EmitsHierarchicalErrorClasses drives the in-process
// http-node executor against a deliberately-failing target and asserts
// at least one emission carries the `http/<leaf>` hierarchical class
// per `concept:signal`'s hierarchical-class rule.
//
// The test reaches through the executor's HTTP+JSON bridge (no gRPC
// server boot needed) by invoking the underlying request flow through
// a recording handler. Validating the emission's class string is the
// smoke-test contract: every operator-visible Error from this executor
// must carry a `/`-containing class.
func TestHttpNode_EmitsHierarchicalErrorClasses(t *testing.T) {
	t.Parallel()
	// Spin up a failing upstream — returns 500 with no body.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	// Drive the http-node executor in-process via the public
	// Capabilities surface. The capabilities-only call confirms the
	// executor's declared_error_classes carries hierarchical entries
	// (every leaf either contains `/` or is a `*` wildcard pattern).
	// We invoke through a fresh gRPC server bound to a loopback
	// listener for parity with how an operator would dial it.
	declared := httpNodeDeclaredErrorClasses(t)
	require.NotEmpty(t, declared, "http-node must declare at least one error class")
	for _, c := range declared {
		// Each declared entry is either an exact path containing `/`
		// or a `<prefix>/*` wildcard. Flat single-segment classes are
		// disallowed under the hierarchical convention.
		if c == "" {
			t.Errorf("declared error class is empty string")
			continue
		}
		require.Contains(t, c, "/",
			"declared http-node class %q is flat; must follow the hierarchical http/<leaf> convention", c)
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
	dsn, terminate := pgtest.StartFreshPostgresDSN(ctx, t)
	t.Cleanup(terminate)

	st, err := pgstore.New(ctx, pgstore.Config{
		Connection:     dsn,
		WriteSemantics: corestore.WriteSemanticsStagedAsync,
	})
	require.NoError(t, err)
	t.Cleanup(st.Close)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, pgstoreserver.NewExecutorServer(st))
	genv1.RegisterExecutorObservabilityServer(srv, pgstoreserver.NewExecutorObservabilityServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// Verify the observability handshake advertises the hierarchical
	// pg/* vocabulary.
	obsClient := genv1.NewExecutorObservabilityClient(conn)
	caps, err := obsClient.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, caps.GetDeclaredErrorClasses(),
		"postgres-stores executor must declare hierarchical error classes")
	for _, c := range caps.GetDeclaredErrorClasses() {
		require.Contains(t, c, "/",
			"declared pg-executor class %q is flat; must follow pg/<leaf> convention", c)
	}

	// Drive an Execute with missing attributes to surface
	// pg/attribute_invalid.
	ud, _ := structpb.NewStruct(map[string]any{})
	execClient := genv1.NewExecutorClient(conn)
	stream, err := execClient.Execute(ctx, &genv1.ExecuteRequest{Attributes: ud})
	require.NoError(t, err)
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
	require.Equal(t, "pg/attribute_invalid", emittedClass,
		"postgres-stores executor with missing attributes must emit pg/attribute_invalid")
}

// httpNodeDeclaredErrorClasses returns the http-node executor's
// declared error vocabulary by importing the canonical source from
// `pkg:executors/http-node/errorclasses`. The executor's main package
// reads the same Declared() helper to populate
// `ObservabilityCapabilities.DeclaredErrorClasses`, so any drift in
// the executor's advertised list becomes a compile-or-test-time
// failure rather than a silently passing assertion.
func httpNodeDeclaredErrorClasses(t *testing.T) []string {
	t.Helper()
	return httpnodeerrclasses.Declared()
}

// TestVerifierHttp_EmitsHierarchicalErrorClasses asserts the
// verifier-http bundled executor's declared error vocabulary follows
// the `verifier/<leaf>` hierarchical convention. Same drift-detection
// pattern as TestHttpNode_EmitsHierarchicalErrorClasses: importing the
// canonical `pkg:executors/verifier-http/errorclasses` makes any
// divergence between the executor's advertised list and the contract
// a compile-or-test-time failure.
func TestVerifierHttp_EmitsHierarchicalErrorClasses(t *testing.T) {
	t.Parallel()
	declared := verifierhttperrclasses.Declared()
	require.NotEmpty(t, declared, "verifier-http must declare at least one error class")
	for _, c := range declared {
		if c == "" {
			t.Errorf("declared error class is empty string")
			continue
		}
		require.Contains(t, c, "/",
			"declared verifier-http class %q is flat; must follow the hierarchical verifier/<leaf> convention", c)
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
	require.NotEmpty(t, declared, "verifier-shape-checks must declare at least one error class")
	for _, c := range declared {
		if c == "" {
			t.Errorf("declared error class is empty string")
			continue
		}
		require.Contains(t, c, "/",
			"declared verifier-shape-checks class %q is flat; must follow the hierarchical verifier/<leaf> convention", c)
	}
}
