// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package clientiface holds the rimsky-side wire-shape interfaces and
// DTO types for the producer-protocol clients (DataProcessing, Sensor,
// Validation). The types live here — separate from the orchestration
// logic in `runtime/` — so that wire-surface consumers (the gRPC
// remote clients in `runtime/peer/`, the conformance binaries under
// `cmd/rimsky-*-conformance/`, and external producer authors) can
// link against them without crossing the runtime/Apache → AGPL
// licensing boundary. Pure interface + DTO shapes; no orchestration.

package clientiface

import "context"

// DataProcessingClient is the rimsky-side wrapper around a producer's
// DataProcessing gRPC client. Bundles or operator-supplied producers
// that advertise the `data_processing` protocol satisfy this surface.
//
// Operations are claim-id-keyed for idempotency per spec §Protocol
// surfaces / DataProcessing. The runtime treats every output bytes
// field (CandidateHandle, CandidateMetadata, VersionID payloads) as
// opaque per @blessed-invariant 20-class.
type DataProcessingClient interface {
	// Name returns the operator-configured producer name (matches the
	// `producer_name` slot in the matching ClaimProducer client).
	Name() string

	// BeginCandidate opens a candidate write on the producer for the
	// given sub-claim. Idempotent in `idempotency_key`. Returns the
	// `candidate_handle` bytes the leaf executor receives in its
	// ExecuteRequest.
	BeginCandidate(ctx context.Context, in BeginCandidateInput) (BeginCandidateOutput, error)

	// CommitCandidate finalizes a candidate. Returns the producer's
	// per-version metadata (opaque bytes).
	CommitCandidate(ctx context.Context, in CommitCandidateInput) (CommitCandidateOutput, error)

	// AbandonCandidate disposes of an outstanding candidate.
	AbandonCandidate(ctx context.Context, in AbandonCandidateInput) error

	// ListVersions enumerates versions associated with a claim handle.
	ListVersions(ctx context.Context, in ListVersionsInput) (ListVersionsOutput, error)

	// ListPartitions enumerates partitions for a (claim_handle, version_id).
	ListPartitions(ctx context.Context, in ListPartitionsInput) (ListPartitionsOutput, error)

	// GetVersionSchema returns the producer-declared schema for a
	// given version. Bytes are opaque to rimsky.
	GetVersionSchema(ctx context.Context, in GetVersionSchemaInput) (GetVersionSchemaOutput, error)
}

// DataProcessingRegistry resolves a producer name to the matching
// DataProcessingClient (when the producer advertises the protocol).
// Returns ok=false when the producer is not configured for data
// processing on this process.
type DataProcessingRegistry interface {
	Get(name string) (DataProcessingClient, bool)
}

// BeginCandidateInput is the rimsky-side payload for BeginCandidate.
// SubScopeDescriptor and IdempotencyKey are pass-through bytes;
// rimsky does not interpret them per @blessed-invariant 20-class.
type BeginCandidateInput struct {
	ProducerName       string
	ClaimHandleID      string
	SubScopeDescriptor []byte
	IdempotencyKey     string
}

// BeginCandidateOutput is the rimsky-side projection of the producer's
// response. CandidateHandle bytes are inert in rimsky.
type BeginCandidateOutput struct {
	CandidateHandle []byte
}

// CommitCandidateInput is the rimsky-side payload for CommitCandidate.
type CommitCandidateInput struct {
	ProducerName    string
	ClaimHandleID   string
	CandidateHandle []byte
}

// CommitCandidateOutput carries the producer-supplied per-version
// metadata that rimsky persists onto the lineage row + the
// rimsky_claim_handles.version_id column.
type CommitCandidateOutput struct {
	VersionID         string
	CandidateMetadata []byte
}

// AbandonCandidateInput is the rimsky-side payload for AbandonCandidate.
type AbandonCandidateInput struct {
	ProducerName    string
	ClaimHandleID   string
	CandidateHandle []byte
}

// ListVersionsInput is the rimsky-side payload for ListVersions.
type ListVersionsInput struct {
	ProducerName  string
	ClaimHandleID string
}

// ListVersionsOutput is the rimsky-side projection of ListVersions.
type ListVersionsOutput struct {
	Versions []DataProcessingVersion
}

// DataProcessingVersion mirrors the proto VersionMetadata.
type DataProcessingVersion struct {
	VersionID        string
	CommittedAtUnixS int64
	ProducerMetadata []byte
}

// ListPartitionsInput is the rimsky-side payload for ListPartitions.
type ListPartitionsInput struct {
	ProducerName  string
	ClaimHandleID string
	VersionID     string
}

// ListPartitionsOutput is the rimsky-side projection of ListPartitions.
type ListPartitionsOutput struct {
	Partitions []DataProcessingPartition
}

// DataProcessingPartition mirrors the proto PartitionDescriptor.
type DataProcessingPartition struct {
	PartitionKey      string
	PartitionMetadata []byte
}

// GetVersionSchemaInput is the rimsky-side payload for GetVersionSchema.
type GetVersionSchemaInput struct {
	ProducerName  string
	ClaimHandleID string
	VersionID     string
}

// GetVersionSchemaOutput carries the producer-supplied schema bytes.
type GetVersionSchemaOutput struct {
	Schema []byte
}
