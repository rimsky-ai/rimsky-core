// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func repoRootForCLI(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", ".."))
}

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

type brokenClaimProducer struct {
	genv1.UnimplementedClaimProducerServer
}

func (*brokenClaimProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
	}, nil
}

func (*brokenClaimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
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
	return nil, status.Error(codes.Internal, "broken producer: Commit deliberately fails")
}
