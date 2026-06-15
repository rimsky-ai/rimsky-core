// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	Name() string

	// @agent-contract: idempotent in `in.IdempotencyKey`; the returned
	// CandidateHandle bytes are the same bytes the leaf executor later
	// receives in its ExecuteRequest.
	BeginCandidate(ctx context.Context, in BeginCandidateInput) (BeginCandidateOutput, error)

	// @agent-contract: returns the producer's per-version metadata as
	// opaque bytes; rimsky does not interpret them.
	CommitCandidate(ctx context.Context, in CommitCandidateInput) (CommitCandidateOutput, error)

	AbandonCandidate(ctx context.Context, in AbandonCandidateInput) error

	ListVersions(ctx context.Context, in ListVersionsInput) (ListVersionsOutput, error)

	ListPartitions(ctx context.Context, in ListPartitionsInput) (ListPartitionsOutput, error)

	// @agent-contract: schema bytes are opaque to rimsky.
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
