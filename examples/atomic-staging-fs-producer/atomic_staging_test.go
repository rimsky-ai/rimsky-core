// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/server"
	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestAtomicStaging_StageThenSwap drives the recovered filesystem
// atomic-staging reference producer end-to-end over gRPC (in the shape
// of claimproducer/claimproducer_test.go) and pins the stage-then-swap
// contract that makes this a real claim-producer rather than a no-op:
//
//   - Open reserves a private staging directory (it exists) while the
//     canonical view for that scope is still absent — nothing is
//     visible to readers until Commit.
//   - The executor writes its work product into staging.
//   - Commit performs a real POSIX rename of staging into the canonical
//     view — the file is now at the canonical path and the staging
//     directory is gone (the swap moved, did not copy).
//   - A second claim's Abandon discards its staging and leaves the
//     committed canonical view untouched.
//
// These properties are the load-bearing safety guarantees of the
// atomic-staging pattern: no partial state is ever visible at the
// canonical path (atomic rename), and an abandoned run cannot corrupt a
// previously-committed view.
func TestAtomicStaging_StageThenSwap(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(root)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	client, stop := startProducer(t, st)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const scope = "tenant-a"

	// Capabilities advertises staged_async — this producer's whole point
	// is to give downstream verifiers a staging area to inspect before
	// Commit fires.
	caps, err := client.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if !advertises(caps, genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC) {
		t.Fatalf("Capabilities must advertise STAGED_ASYNC, got %v", caps.GetWriteSemanticsAllowed())
	}

	// --- Open reserves a private staging dir; canonical view absent. ---
	openResp, err := client.Open(ctx, &genv1.OpenRequest{
		ClaimId:  "claim-1",
		Selector: scope,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("open claim-1: %v", err)
	}
	acq := openResp.GetAcquired()
	if acq == nil {
		t.Fatalf("Open did not return Acquired: %+v", openResp.GetResult())
	}
	stagingPath := string(acq.GetAddress())
	if stagingPath == "" {
		t.Fatal("Open returned an empty staging address")
	}
	if acq.GetRealizedWriteSemantics() != genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC {
		t.Fatalf("Open realized write-semantics = %v, want STAGED_ASYNC", acq.GetRealizedWriteSemantics())
	}
	if !dirExists(stagingPath) {
		t.Fatalf("Open did not reserve the staging directory %q", stagingPath)
	}
	canonicalPath := filepath.Join(root, "canonical", scope)
	if pathExists(canonicalPath) {
		t.Fatalf("canonical view %q must be absent until Commit", canonicalPath)
	}

	// --- The executor writes its work product into staging. ---
	const fileName = "result.txt"
	const payload = "committed-bytes"
	if err := os.WriteFile(filepath.Join(stagingPath, fileName), []byte(payload), 0o644); err != nil {
		t.Fatalf("write into staging: %v", err)
	}

	// --- Commit: atomic rename of staging into the canonical view. ---
	if _, err := client.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    "claim-1",
		ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("commit claim-1: %v", err)
	}
	// The file is now visible at the canonical path with its exact bytes.
	got, err := os.ReadFile(filepath.Join(canonicalPath, fileName))
	if err != nil {
		t.Fatalf("read committed file at canonical path: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("committed bytes = %q, want %q", string(got), payload)
	}
	// The swap was a real rename, not a copy: the staging directory is
	// gone after Commit.
	if dirExists(stagingPath) {
		t.Fatalf("staging dir %q must be gone after Commit (rename, not copy)", stagingPath)
	}

	// --- A fresh claim's Abandon leaves the committed view untouched. ---
	openResp2, err := client.Open(ctx, &genv1.OpenRequest{
		ClaimId:  "claim-2",
		Selector: scope,
		Intent:   "rw",
	})
	if err != nil {
		t.Fatalf("open claim-2: %v", err)
	}
	staging2 := string(openResp2.GetAcquired().GetAddress())
	if !dirExists(staging2) {
		t.Fatalf("Open claim-2 did not reserve staging %q", staging2)
	}
	// Write a would-be-clobbering payload into the second staging area,
	// then Abandon it.
	if err := os.WriteFile(filepath.Join(staging2, fileName), []byte("abandoned-bytes"), 0o644); err != nil {
		t.Fatalf("write into staging2: %v", err)
	}
	if _, err := client.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId:    "claim-2",
		ClaimScope: []byte(scope),
	}); err != nil {
		t.Fatalf("abandon claim-2: %v", err)
	}
	// Abandon discarded the staged work...
	if dirExists(staging2) {
		t.Fatalf("staging dir %q must be discarded after Abandon", staging2)
	}
	// ...and the committed canonical view is unchanged.
	after, err := os.ReadFile(filepath.Join(canonicalPath, fileName))
	if err != nil {
		t.Fatalf("read canonical file after Abandon: %v", err)
	}
	if string(after) != payload {
		t.Fatalf("canonical bytes after Abandon = %q, want unchanged %q", string(after), payload)
	}
}

// startProducer stands up the server.Server in-process over a loopback
// gRPC listener and returns a connected ClaimProducer client plus a stop
// func. Mirrors claimproducer/claimproducer_test.go.
func startProducer(t *testing.T, st *store.Store) (genv1.ClaimProducerClient, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, server.New(st))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		srv.Stop()
		t.Fatalf("dial: %v", err)
	}
	stop := func() {
		_ = conn.Close()
		srv.Stop()
	}
	return genv1.NewClaimProducerClient(conn), stop
}

func advertises(caps *genv1.CapabilitiesResponse, sem genv1.WriteSemantics) bool {
	for _, s := range caps.GetWriteSemanticsAllowed() {
		if s == sem {
			return true
		}
	}
	return false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
