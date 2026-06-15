// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end coverage of the `rimsky conformance claim-producer` SUBCOMMAND's
// terminal/idempotency rows + exit codes — the CLI/acceptance leg of story
// S-conformance-claimproducer-terminals.
//
// The in-package conformance test (pg_verifier_conformance_test.go) asserts the
// runner library's `[]CheckResult` contains passing terminal rows. This test
// closes the loop at the SHIPPED SURFACE the operator actually runs: it builds
// the real `rimsky` binary, points the `conformance claim-producer` subcommand
// at a live producer over gRPC, and asserts the printed pass/fail rows and the
// process exit code that an operator (or CI) keys on.
//
// Two halves, per the acceptance:
//
//  1. REAL producer — the bundled fused postgres claim-producer (booted
//     in-process via the atomic_staging harness's `startPgStore`, the same
//     real value-delivering producer the in-package conformance test exercises)
//     over a real testcontainers Postgres. The CLI MUST exit 0 and print a
//     passing `ok    Commit` / `ok    Abandon` / `ok    Release` /
//     `ok    TerminalIdempotency` row for each terminal verb.
//
//  2. BROKEN producer — a tiny in-test gRPC server whose `Commit` returns a
//     status error (Open returns Acquired so the suite reaches the terminal
//     probe; Abandon/Release are not exercised because the broken half asserts
//     only the Commit FAIL row). The CLI MUST exit non-zero and print
//     `FAIL  Commit` — proving the real CLI exit-code logic
//     (conformance.go's `return 1` on any failed row) is wired end to end.
//
// Requires Docker (the real-producer half uses a Postgres testcontainer); the
// broken-producer half is pure in-process gRPC and needs no Docker.
package atomicstaging

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestConformanceClaimProducerTerminalsCLI builds the `rimsky` CLI and runs its
// `conformance claim-producer` subcommand against a real producer (exit 0 +
// passing terminal rows) and a deliberately-broken producer (non-zero exit +
// `FAIL  Commit`). The CLI binary, the real fused producer, and the gRPC
// transport are all real — the test asserts the operator-observable shipped
// surface, not the runner library in isolation.
func TestConformanceClaimProducerTerminalsCLI(t *testing.T) {
	t.Parallel()

	cliPath := buildRimskyCLI(t)

	t.Run("real_producer_passes", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		dsn := harness.StartFreshPostgres(ctx, t)
		endpoint, teardown := startPgStore(t, dsn, true)
		t.Cleanup(teardown)

		stdout, exitCode := runConformanceCLI(t, cliPath, "grpc://"+endpoint)

		if exitCode != 0 {
			t.Fatalf("conformance CLI against real producer exited %d (want 0)\nstdout:\n%s",
				exitCode, stdout)
		}
		for _, want := range []string{
			"ok    Commit",
			"ok    Abandon",
			"ok    Release",
			"ok    TerminalIdempotency",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("conformance CLI stdout missing %q against real producer\nstdout:\n%s",
					want, stdout)
			}
		}
	})

	t.Run("broken_commit_fails", func(t *testing.T) {
		t.Parallel()
		endpoint := startBrokenClaimProducer(t)

		stdout, exitCode := runConformanceCLI(t, cliPath, "grpc://"+endpoint)

		if exitCode == 0 {
			t.Fatalf("conformance CLI against broken producer exited 0 (want non-zero)\nstdout:\n%s",
				stdout)
		}
		if !strings.Contains(stdout, "FAIL  Commit") {
			t.Errorf("conformance CLI stdout missing %q against broken producer\nstdout:\n%s",
				"FAIL  Commit", stdout)
		}
	})
}

// buildRimskyCLI compiles cmd/rimsky into a temp binary and returns its path.
// The build runs from the repo root so the root module's go.mod governs (the
// CLI is in the root module; this test's package is in the lib/services module).
func buildRimskyCLI(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "rimsky")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/rimsky")
	cmd.Dir = repoRootForCLI(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/rimsky: %v\nstderr:\n%s", err, stderr.String())
	}
	return out
}

// repoRootForCLI returns the rimsky-core repo root from this test file's
// location. This file lives at lib/services/test/scenarios/atomic_staging/, five
// directories below the root. The harness package's own repoRoot() is
// unexported, so we resolve our own copy here rather than couple to it.
func repoRootForCLI(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", ".."))
}

// runConformanceCLI runs `rimsky conformance claim-producer --endpoint <ep>` as
// a subprocess and returns its combined stdout (the pass/fail rows print to
// stdout) and process exit code. A non-zero exit is NOT a test failure here —
// the caller asserts the expected code, since the broken-producer half expects
// a non-zero exit.
func runConformanceCLI(t *testing.T, cliPath, endpoint string) (stdout string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliPath,
		"conformance", "claim-producer", "--endpoint", endpoint)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	t.Logf("conformance CLI %s\nstdout:\n%s\nstderr:\n%s", endpoint, out.String(), errBuf.String())
	if err == nil {
		return out.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("conformance CLI run error (not an exit error): %v", err)
	}
	return out.String(), exitErr.ExitCode()
}

// startBrokenClaimProducer stands up a minimal in-process gRPC ClaimProducer
// whose Commit returns a status error. It advertises sync write-semantics (so
// the staged_async-only Serialization9b probe SKIPs) and Open returns Acquired,
// so the conformance runner reaches the terminal probe and reports a failing
// Commit row. Returns the dial address; the server is torn down on cleanup.
func startBrokenClaimProducer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("broken producer listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, &brokenClaimProducer{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.GracefulStop()
		wg.Wait()
	})
	return lis.Addr().String()
}

// brokenClaimProducer is a deliberately-broken ClaimProducer for the FAIL-path
// leg: Open succeeds (Acquired) so the suite reaches the terminal probe, but
// Commit returns a status error. Abandon/Release/SplitScope/ScopesConflict fall
// through to the embedded Unimplemented* server — the broken-producer half
// asserts only the Commit FAIL row, which is what the CLI exit-code logic keys
// the non-zero exit on.
type brokenClaimProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (*brokenClaimProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	// @deliberate: advertise sync write-semantics so the staged_async-specific
	// Serialization9b probe stays in its SKIP path; the broken half asserts only
	// the Commit FAIL row.
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func (*brokenClaimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	// @deliberate: return Acquired on every Open with a claim_scope that echoes
	// the selector (byte-stable per identical selector, satisfying the
	// uniformity precondition) so the conformance runner reaches the terminal
	// probe and the failing Commit row is what the CLI exit-code logic keys on.
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				ClaimScope:             []byte(req.GetSelector()),
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			},
		},
	}, nil
}

func (*brokenClaimProducer) Commit(context.Context, *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	// @deliberate: always return a status error from Commit so the conformance
	// runner emits a failing Commit row and the CLI exits non-zero — the FAIL
	// path the test asserts on.
	return nil, status.Error(codes.Internal, "broken producer: Commit deliberately fails")
}
