// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// main_test.go drives the Validation conformance suite against an
// in-process Validation server. The self-test uses a tiny inline
// implementation that mirrors executors/verifier-shape-checks/
// validation semantics (the cmd test package cannot import a `main`
// package, so the validator logic is re-shaped here under role
// dispatch). Wire conformance against the bundled binary is
// exercised in the smoke fixture (O1).

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// fixtureValidationServer is a minimal Validation impl mirroring the
// verifier-shape-checks validator. The cmd test package cannot
// import the verifier-shape-checks main package; this stand-in is
// the conformance contract reduced to its essentials.
type fixtureValidationServer struct {
	genv1.UnimplementedValidationServer
}

func (s *fixtureValidationServer) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	if req.GetRole() != "executor" {
		return &genv1.ValidateResponse{
			Valid: false,
			Errors: []*genv1.ValidationFinding{{
				Class:   "unsupported_role",
				Message: fmt.Sprintf("fixture: role %q not supported", req.GetRole()),
				Path:    "/role",
			}},
		}, nil
	}
	exec := req.GetExecutor()
	if exec == nil {
		return &genv1.ValidateResponse{
			Valid: false,
			Errors: []*genv1.ValidationFinding{{
				Class:   "missing_context",
				Message: "fixture: ValidateRequest.context.executor must be set for role=executor",
				Path:    "/executor",
			}},
		}, nil
	}
	var attrs map[string]any
	if len(exec.GetAttributesSchema()) > 0 {
		if err := json.Unmarshal(exec.GetAttributesSchema(), &attrs); err != nil {
			return &genv1.ValidateResponse{
				Valid: false,
				Errors: []*genv1.ValidationFinding{{
					Class:   "invalid_attributes_schema",
					Message: fmt.Sprintf("fixture: attributes_schema not valid JSON: %v", err),
					Path:    "/executor/attributes_schema",
				}},
			}, nil
		}
	}
	_ = attrs
	return &genv1.ValidateResponse{Valid: true}, nil
}

// startFixtureServer spins up a Validation server on an ephemeral
// loopback listener and returns the endpoint + teardown.
func startFixtureServer(t *testing.T) (endpoint string, teardown func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterValidationServer(srv, &fixtureValidationServer{})
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(lis)
		close(done)
	}()
	return lis.Addr().String(), func() {
		srv.GracefulStop()
		<-done
	}
}

// TestValidationConformance_Executor drives the suite against the
// fixture Validation server with --role=executor.
func TestValidationConformance_Executor(t *testing.T) {
	endpoint, teardown := startFixtureServer(t)
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewValidationClient(conn)

	results := RunValidationConformance(context.Background(), client, "executor")
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
	// Pin: every executor-role check name appears in results.
	want := map[string]bool{
		"ExecutorHappy":                     false,
		"ExecutorMalformedAttributesSchema": false,
		"ExecutorMissingContext":            false,
		"UnknownRole":                       false,
	}
	for _, r := range results {
		if _, ok := want[r.Name]; ok {
			want[r.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected check %q to run, did not see it", name)
		}
	}
}

// TestValidationConformance_UnsupportedRole asserts the dispatcher
// surfaces a precise error when an unknown role is requested.
func TestValidationConformance_UnsupportedRole(t *testing.T) {
	endpoint, teardown := startFixtureServer(t)
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewValidationClient(conn)

	results := RunValidationConformance(context.Background(), client, "totally-fake-role")
	if len(results) == 0 || results[0].Name != "RoleDispatch" || results[0].Err == nil {
		t.Fatalf("expected RoleDispatch error, got %+v", results)
	}
}
