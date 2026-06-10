// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-data-processing-author: a service author's
// example DataProcessing mix-in — advertising Capabilities and serving the
// candidate lifecycle (BeginCandidate / CommitCandidate / AbandonCandidate)
// plus the version-history surfaces (ListVersions / ListPartitions /
// GetVersionSchema) — exhibits each protocol surface through the EXACT
// wire shape rimsky's supervisor uses against a remote DataProcessing peer
// (lib/runtime/peer.DialDataProcessing + clientiface.DataProcessingClient).
//
// The cross-stack BeginCandidate-per-fan-out-partition leg of the spec's
// Acceptance is covered end-to-end against a real rimsky-all-in-one stack
// by test/scenarios/leaf_candidate_handle_e2e_test.go: that scenario
// declares a fan-out node referencing a remote stub store whose
// DataProcessing surface (test/support/stores/stub/dataprocessing) mints
// one candidate per BeginCandidate, and asserts each of the three fan-out
// leaves dispatches with a non-empty per-partition-unique
// candidate_handle on its StoreHandle. That scenario pins the "rimsky
// calls BeginCandidate per sub-partition" leg through the assembled
// product; the test here pins the protocol-surface behavior of the
// EXAMPLE producer against that same wire shape, completing the four
// observable legs the spec's Falsifier names:
//
//  1. BeginCandidate is called and returns a non-empty candidate_handle
//     for a fan-out partition (exhibited per-handler-call here; the
//     cross-stack rimsky-side dispatch is exhibited by
//     leaf_candidate_handle_e2e_test.go against the stub store fixture
//     whose DataProcessing surface mirrors this example).
//  2. CommitCandidate moves the staged candidate into the per-claim
//     version history with a fresh version_id, surfaces opaque metadata
//     to the caller, and bumps the example's CommitCount counter — proof
//     "CommitCandidate is called but the producer's effect is canned"
//     is FALSE (counter does not grow against a canned handler).
//  3. AbandonCandidate is NOT skipped on leaf failure: a Begin →
//     Abandon sequence drives AbandonCount up and clears the staged
//     entry. The follow-up ListVersions read returns an empty list —
//     proof an abandoned candidate is NOT silently committed.
//  4. The version_id CommitCandidate's metadata declares appears in the
//     subsequent ListVersions response, ListPartitions returns the
//     partition descriptor the example seeds at commit time, and
//     GetVersionSchema returns the producer-declared schema bytes —
//     proof "a declared version doesn't appear in ListVersions" is
//     FALSE.
//
// Test files are exempt from the Apache→AGPL import-direction lint
// (tools/license-check/imports.go::verifyImports), so this `_test.go`
// file may import lib/runtime/peer (AGPL) without putting the example's
// published Apache surface at risk — consumers who `go build` the
// example never pull in any test dependency.
package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// TestE2E_ExampleDataProcessingProtocolSurfaces drives the example
// producer through the EXACT rimsky-side client surface
// (lib/runtime/peer.DialDataProcessing — the same constructor
// lib/runtime/peer/registry dials at startup for any operator-declared
// claim_producer that advertises the `data_processing` protocol) and
// asserts each falsifier-named observation lands.
//
// The four legs each run against the SAME running in-process server so a
// failure can be diagnosed against a single endpoint:
//
//	─ Capabilities advertises a non-empty set
//	─ BeginCandidate + CommitCandidate land + the version surfaces in
//	  ListVersions / ListPartitions / GetVersionSchema
//	─ BeginCandidate + AbandonCandidate land + the version is NOT in
//	  ListVersions (the failure path is honored, not silently committed)
//	─ The cross-stack rimsky-side BeginCandidate per fan-out partition
//	  is exhibited by test/scenarios/leaf_candidate_handle_e2e_test.go
//	  against a stub store whose DataProcessing surface mirrors this
//	  example
//
// No rimsky-all-in-one bring-up is needed: the cross-stack dispatch
// path is already exhibited end-to-end by the leaf-candidate-handle
// scenario referenced above, so this test pins the protocol-surface
// behavior of THIS example through the same rimsky-side client (no
// re-dispatch through testcontainers). Total wall time: <1s.
func TestE2E_ExampleDataProcessingProtocolSurfaces(t *testing.T) {
	t.Parallel()

	// 1. In-process example DataProcessing server on a free loopback port.
	dp, endpoint, stop := startExampleDataProcessing(t)
	defer stop()

	// 2. Dial the example through the EXACT rimsky-side client
	//    constructor. peer.DialDataProcessing is what
	//    lib/runtime/peer/registry runs at rimsky startup for every
	//    operator-declared claim_producer that lists "data_processing"
	//    in its protocols block; the returned client satisfies
	//    clientiface.DataProcessingClient, the same interface
	//    lib/runtime/data_processing.go drives against during fan-out
	//    sub-claim acquisition.
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := peer.DialDataProcessing(dialCtx, "example", endpoint)
	if err != nil {
		t.Fatalf("peer.DialDataProcessing(%q): %v", endpoint, err)
	}
	defer client.Close()

	// Compile-time sanity check: the dialed client really is the
	// interface rimsky's runtime drives against. A future signature
	// change in clientiface that breaks this assignment surfaces here
	// at compile time, not as a silent drift between this example and
	// the rimsky-side wiring.
	var _ clientiface.DataProcessingClient = client

	// Each leg runs as a sub-test against the SAME running example so
	// the four legs share a single bring-up. The legs are independent
	// observations against distinct claim handles, so order does not
	// matter; t.Run keeps the failure isolation per-leg.

	t.Run("Capabilities_advertises_non_empty_set", func(t *testing.T) {
		exerciseCapabilitiesLeg(t, endpoint)
	})

	t.Run("BeginCommit_lands_and_surfaces_in_ListVersions", func(t *testing.T) {
		exerciseBeginCommitListLeg(t, dp, client)
	})

	t.Run("BeginAbandon_lands_and_does_NOT_surface_in_ListVersions", func(t *testing.T) {
		exerciseBeginAbandonLeg(t, dp, client)
	})
}

// exerciseCapabilitiesLeg dials the example via a vanilla gRPC client
// (NOT the rimsky-side wrapper, which doesn't expose Capabilities — the
// startup discovery cache reads it from the underlying ClaimProducer
// peer's CapabilitiesResponse.protocols envelope; the example here is
// the DataProcessing mix-in surface, so we read its Capabilities
// directly). Asserts the advertised capability set is non-empty, the
// Falsifier-adjacent observable for "the producer advertises something
// to gate against": a producer that returns an empty set is silently
// non-functional.
func exerciseCapabilitiesLeg(t *testing.T, endpoint string) {
	t.Helper()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial example for Capabilities: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	caps, err := genv1.NewDataProcessingClient(conn).Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	// The example advertises one entry in each of the four envelope
	// fields. A real producer might be sparser; the load-bearing
	// observation is "non-empty" — an empty set means the producer
	// materializes nothing.
	if len(caps.GetDataShapes()) == 0 && len(caps.GetMaterializations()) == 0 &&
		len(caps.GetPartitionKinds()) == 0 && len(caps.GetAggregators()) == 0 {
		t.Fatal("Capabilities advertised an empty capability set — a producer that materializes nothing is silently non-functional")
	}
}

// exerciseBeginCommitListLeg drives the rimsky-side fan-out-leaf success
// path against the example:
//
//	BeginCandidate("claim-success", "tenant/a/2026-06", "run-success-1")
//	  → CommitCandidate(handle) → metadata.version_id is recorded
//	    → ListVersions("claim-success") includes the version
//	    → ListPartitions(claim, version) returns the partition descriptor
//	    → GetVersionSchema(claim, version) returns non-empty bytes
//
// Asserts:
//  1. BeginCandidate returns a non-empty candidate_handle (falsifier:
//     BeginCandidate never called / canned).
//  2. CommitCandidate's effect is real: CommitCount grows and the
//     metadata carries a version_id (falsifier: "CommitCandidate is
//     called but the producer's effect is canned" fails when the
//     counter doesn't grow or the metadata is empty).
//  3. ListVersions returns the version_id CommitCandidate just declared
//     (falsifier: "a declared version doesn't appear in ListVersions"
//     fails here).
//  4. ListPartitions returns the partition descriptor the example
//     seeds at commit time, keyed by the sub-scope BeginCandidate
//     received — a canned partition list (one that ignores the
//     sub-scope) would fail the key check.
//  5. GetVersionSchema returns non-empty schema bytes — a canned
//     handler that returns nothing fails the length check.
func exerciseBeginCommitListLeg(t *testing.T, dp *DataProcessing, client *peer.DataProcessingClient) {
	t.Helper()
	const claimID = "claim-success"
	const subScope = "tenant/a/2026-06"

	beforeCommit := dp.CommitCount()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	beginOut, err := client.BeginCandidate(ctx, clientiface.BeginCandidateInput{
		ProducerName:       "example",
		ClaimHandleID:      claimID,
		SubScopeDescriptor: []byte(subScope),
		IdempotencyKey:     "run-success-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if len(beginOut.CandidateHandle) == 0 {
		t.Fatal("BeginCandidate returned an empty candidate_handle — rimsky persists this on " +
			"col:rimsky_claim_handles.producer_candidate_handle and the leaf dispatch reads it " +
			"back on StoreHandle.candidate_handle; an empty handle would make the leaf dispatch " +
			"with no producer cursor, the falsifier for \"BeginCandidate is never called on a " +
			"fan-out partition\" in canned-handler form")
	}

	commitOut, err := client.CommitCandidate(ctx, clientiface.CommitCandidateInput{
		ProducerName:    "example",
		ClaimHandleID:   claimID,
		CandidateHandle: beginOut.CandidateHandle,
	})
	if err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	if afterCommit := dp.CommitCount(); afterCommit != beforeCommit+1 {
		t.Fatalf("CommitCount did not grow against the live handler: before=%d after=%d — "+
			"the rimsky-side client returned OK but the producer's effect was canned "+
			"(falsifier: \"CommitCandidate is called but the producer's effect is canned\")",
			beforeCommit, afterCommit)
	}
	if len(commitOut.CandidateMetadata) == 0 {
		t.Fatal("CommitCandidate returned empty candidate_metadata — the producer surfaces " +
			"the per-version metadata via the parent's writeback; empty metadata would mean " +
			"the rimsky-side commit lands but the producer's metadata is canned")
	}
	// Decode the example's metadata blob to extract the version_id the
	// producer declared. A real consumer (operator, dashboard) reads
	// this through ListVersions; the example threads it through
	// metadata too so the proof can correlate the surface-level read
	// against the commit-time declaration.
	var meta struct {
		VersionID string `json:"version_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(commitOut.CandidateMetadata, &meta); err != nil {
		t.Fatalf("decode CommitCandidate metadata %q: %v", string(commitOut.CandidateMetadata), err)
	}
	if meta.Status != "committed" {
		t.Fatalf("CommitCandidate metadata.status=%q, want %q (the example's commit-time marker)",
			meta.Status, "committed")
	}
	if meta.VersionID == "" {
		t.Fatal("CommitCandidate metadata.version_id is empty — the example declares it at commit " +
			"time, so an empty value would mean the producer's effect is canned")
	}

	// ListVersions must return the version the commit just declared —
	// the falsifier "a declared version doesn't appear in ListVersions"
	// fails when this list is empty or missing the declared version_id.
	lvOut, err := client.ListVersions(ctx, clientiface.ListVersionsInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
	})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if !versionListed(lvOut.Versions, meta.VersionID) {
		t.Fatalf("declared version_id %q is missing from ListVersions response %+v — "+
			"falsifier: \"a declared version doesn't appear in ListVersions\"",
			meta.VersionID, lvOut.Versions)
	}

	// ListPartitions must return the partition descriptor the example
	// seeds at commit time, keyed by the sub-scope BeginCandidate
	// received. A canned partition list that ignores the sub-scope
	// would fail the key check.
	lpOut, err := client.ListPartitions(ctx, clientiface.ListPartitionsInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
		VersionID:     meta.VersionID,
	})
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(lpOut.Partitions) == 0 {
		t.Fatal("ListPartitions returned an empty partition list for a version the example just " +
			"committed against a non-empty sub-scope")
	}
	if !partitionKeyed(lpOut.Partitions, subScope) {
		t.Fatalf("ListPartitions returned %+v, none of which carry partition_key=%q — the example "+
			"seeds the partition from BeginCandidate's sub_scope_descriptor, so a missing key "+
			"means the partition list is canned",
			lpOut.Partitions, subScope)
	}

	// GetVersionSchema must return non-empty schema bytes — the
	// example seeds an illustrative JSON Schema at commit; a canned
	// handler that returns nothing fails the length check.
	gsOut, err := client.GetVersionSchema(ctx, clientiface.GetVersionSchemaInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
		VersionID:     meta.VersionID,
	})
	if err != nil {
		t.Fatalf("GetVersionSchema: %v", err)
	}
	if len(gsOut.Schema) == 0 {
		t.Fatal("GetVersionSchema returned an empty schema for a version the example just committed " +
			"— the example seeds a JSON Schema at commit time so an empty response means the " +
			"producer's effect is canned")
	}
	// A producer that returns canned non-JSON bytes would surface here:
	// the example seeds JSON Schema, so the response must parse as
	// JSON. The check is a weak shape gate, not a JSON-Schema parse.
	if !json.Valid(gsOut.Schema) {
		t.Fatalf("GetVersionSchema returned non-JSON schema bytes %q — the example seeds a JSON "+
			"Schema at commit, so non-JSON bytes mean the producer is returning a stale or "+
			"canned blob", string(gsOut.Schema))
	}
}

// exerciseBeginAbandonLeg drives the rimsky-side fan-out-leaf failure
// path against the example:
//
//	BeginCandidate("claim-abandon", "tenant/b/2026-06", "run-abandon-1")
//	  → AbandonCandidate(handle) → AbandonCount grows
//	    → ListVersions("claim-abandon") is EMPTY (the candidate was
//	      abandoned, not silently committed)
//
// Asserts:
//  1. BeginCandidate returns a non-empty candidate_handle (same observable as
//     the commit leg — a canned handler would fail the same check).
//  2. AbandonCandidate succeeds and bumps AbandonCount — the falsifier
//     "AbandonCandidate is skipped on leaf failure" fails when the
//     counter does not grow against a live failure path.
//  3. ListVersions returns an empty list — an Abandon that silently
//     promoted the candidate into versions would fail this check.
//  4. Repeating AbandonCandidate on the same (now-cleared) handle is a
//     no-op success (idempotency under supervisor retry).
func exerciseBeginAbandonLeg(t *testing.T, dp *DataProcessing, client *peer.DataProcessingClient) {
	t.Helper()
	const claimID = "claim-abandon"
	const subScope = "tenant/b/2026-06"

	beforeAbandon := dp.AbandonCount()
	beforeCommit := dp.CommitCount()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	beginOut, err := client.BeginCandidate(ctx, clientiface.BeginCandidateInput{
		ProducerName:       "example",
		ClaimHandleID:      claimID,
		SubScopeDescriptor: []byte(subScope),
		IdempotencyKey:     "run-abandon-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if len(beginOut.CandidateHandle) == 0 {
		t.Fatal("BeginCandidate returned an empty candidate_handle on the abandon leg")
	}

	if err := client.AbandonCandidate(ctx, clientiface.AbandonCandidateInput{
		ProducerName:    "example",
		ClaimHandleID:   claimID,
		CandidateHandle: beginOut.CandidateHandle,
	}); err != nil {
		t.Fatalf("AbandonCandidate: %v", err)
	}
	if afterAbandon := dp.AbandonCount(); afterAbandon != beforeAbandon+1 {
		t.Fatalf("AbandonCount did not grow against the live handler: before=%d after=%d — "+
			"falsifier: \"AbandonCandidate is skipped on leaf failure\" fails when the verb "+
			"reaches the wire but the producer's effect is canned",
			beforeAbandon, afterAbandon)
	}
	if afterCommit := dp.CommitCount(); afterCommit != beforeCommit {
		t.Fatalf("CommitCount changed across an Abandon-only leg: before=%d after=%d — "+
			"an Abandon must NOT silently promote the candidate into a committed version",
			beforeCommit, afterCommit)
	}

	// ListVersions for the abandoned claim must be empty: no version
	// was ever committed against it.
	lvOut, err := client.ListVersions(ctx, clientiface.ListVersionsInput{
		ProducerName:  "example",
		ClaimHandleID: claimID,
	})
	if err != nil {
		t.Fatalf("ListVersions(%q) after abandon: %v", claimID, err)
	}
	if len(lvOut.Versions) != 0 {
		t.Fatalf("ListVersions(%q) returned %+v after AbandonCandidate, want empty — an abandoned "+
			"candidate must NOT surface in the version history",
			claimID, lvOut.Versions)
	}

	// Repeat Abandon: an idempotent no-op success. The supervisor may
	// retry a terminal verb on a partial outage, so the producer MUST
	// tolerate a second Abandon on the same (now-cleared) handle.
	// AbandonCount does NOT grow because the handle is no longer
	// staged; this is the documented behavior in dataprocessing.go's
	// AbandonCandidate comment.
	if err := client.AbandonCandidate(ctx, clientiface.AbandonCandidateInput{
		ProducerName:    "example",
		ClaimHandleID:   claimID,
		CandidateHandle: beginOut.CandidateHandle,
	}); err != nil {
		t.Fatalf("AbandonCandidate (repeat): %v — the producer must tolerate a retried "+
			"terminal verb on an unknown handle", err)
	}
}

// --- helpers ---------------------------------------------------------------

// startExampleDataProcessing stands up the example DataProcessing server
// on a loopback port and returns the producer, the endpoint string, and a
// teardown closure. The server is brought up before the function returns
// (poll-dial gate) so callers do not race the listener.
func startExampleDataProcessing(t *testing.T) (*DataProcessing, string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	dp := newDataProcessing()
	genv1.RegisterDataProcessingServer(srv, dp)
	go func() { _ = srv.Serve(lis) }()

	// Poll-dial gate: the test's first peer.DialDataProcessing call
	// returns immediately because grpc.NewClient is non-blocking, but
	// the first actual RPC would race the listener if we didn't wait.
	// 100 ms / 100 attempts mirrors the pattern in
	// examples/claimproducer/main_e2e_test.go::startExampleProducerInProcess.
	endpoint := lis.Addr().String()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", endpoint, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			stop := func() { srv.Stop() }
			return dp, endpoint, stop
		}
		time.Sleep(25 * time.Millisecond)
	}
	srv.Stop()
	t.Fatalf("in-process example DataProcessing did not become dialable at %s within 10s", endpoint)
	return nil, "", func() {}
}

// versionListed reports whether `want` appears as the VersionID of any
// entry in `got`.
func versionListed(got []clientiface.DataProcessingVersion, want string) bool {
	for _, v := range got {
		if v.VersionID == want {
			return true
		}
	}
	return false
}

// partitionKeyed reports whether `want` appears as the PartitionKey of any
// entry in `got`.
func partitionKeyed(got []clientiface.DataProcessingPartition, want string) bool {
	for _, p := range got {
		if p.PartitionKey == want {
			return true
		}
	}
	return false
}
