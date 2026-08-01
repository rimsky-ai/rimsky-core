// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	valconformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/validation"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

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

func startFixtureValidationServer(t *testing.T) (endpoint string, teardown func()) {
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

func TestValidationConformance_Executor(t *testing.T) {
	endpoint, teardown := startFixtureValidationServer(t)
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewValidationClient(conn)

	results := valconformance.Run(context.Background(), client, "executor")
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}
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

func TestValidationConformance_UnsupportedRole(t *testing.T) {
	endpoint, teardown := startFixtureValidationServer(t)
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := genv1.NewValidationClient(conn)

	results := valconformance.Run(context.Background(), client, "totally-fake-role")
	if len(results) == 0 || results[0].Name != "RoleDispatch" || results[0].Err == nil {
		t.Fatalf("expected RoleDispatch error, got %+v", results)
	}
}
