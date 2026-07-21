// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestCapabilities_AdvertisesSplitScopeAndDeclaredErrorClasses(t *testing.T) {
	srv, _, _ := newServerWithStore(t)
	resp, err := srv.Capabilities(context.Background(), &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !resp.GetSupportsSplitScope() {
		t.Fatal("Capabilities: SupportsSplitScope = false, want true")
	}
	if len(resp.GetDeclaredErrorClasses()) == 0 {
		t.Fatal("Capabilities: DeclaredErrorClasses is empty")
	}
}

func TestOpen_RejectsInvalidIntent(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	_, err := srv.Open(context.Background(), &genv1.OpenRequest{ClaimId: "c1", Selector: "f.txt", Intent: "bogus"})
	if err == nil {
		t.Fatal("Open: expected an error for intent \"bogus\", got nil")
	}
}

func TestOpen_AcquiredEnvelopeCarriesRealizedWriteSemantics(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	resp, err := srv.Open(context.Background(), &genv1.OpenRequest{ClaimId: "c1", Selector: "f.txt", Intent: "rw"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := resp.GetAcquired()
	if acquired == nil {
		t.Fatalf("Open: expected Acquired result, got %+v", resp)
	}
	if acquired.GetRealizedWriteSemantics() != genv1.WriteSemantics_WRITE_SEMANTICS_SYNC {
		t.Fatalf("Open: RealizedWriteSemantics = %v, want SYNC", acquired.GetRealizedWriteSemantics())
	}
	if len(acquired.GetAddress()) == 0 {
		t.Fatal("Open: Acquired.Address is empty")
	}
}

func TestCommit_RoundTripsAfterOpen(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	openResp, err := srv.Open(context.Background(), &genv1.OpenRequest{ClaimId: "c1", Selector: "f.txt", Intent: "rw"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := openResp.GetAcquired()
	if _, err := srv.Commit(context.Background(), &genv1.CommitRequest{
		ClaimId: "c1", ClaimScope: acquired.GetClaimScope(), Address: acquired.GetAddress(),
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestAbandon_RoundTripsAfterOpen(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	openResp, err := srv.Open(context.Background(), &genv1.OpenRequest{ClaimId: "c1", Selector: "f.txt", Intent: "rw"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	acquired := openResp.GetAcquired()
	if _, err := srv.Abandon(context.Background(), &genv1.AbandonRequest{
		ClaimId: "c1", ClaimScope: acquired.GetClaimScope(), Address: acquired.GetAddress(),
	}); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
}

func TestRelease_RoundTripsAfterOpen(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	if _, err := srv.Open(context.Background(), &genv1.OpenRequest{ClaimId: "c1", Selector: "f.txt", Intent: "rw"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := srv.Release(context.Background(), &genv1.ReleaseRequest{ClaimId: "c1"}); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestCommit_RootUnavailableCarriesErrorInfoClass(t *testing.T) {
	srv, _, root := newServerWithStore(t)
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove root: %v", err)
	}
	_, err := srv.Commit(context.Background(), &genv1.CommitRequest{ClaimId: "c1"})
	if err == nil {
		t.Fatal("Commit: expected an error when the configured root is unavailable, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Commit: error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Fatalf("Commit: status code = %v, want Internal", st.Code())
	}
	foundErrorInfo := false
	for _, d := range st.Details() {
		if _, ok := d.(interface{ GetReason() string }); ok {
			foundErrorInfo = true
		}
	}
	if !foundErrorInfo {
		t.Fatalf("Commit: expected an ErrorInfo detail naming the error class, got details=%v", st.Details())
	}
}
