// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// data_processing.go — runtime-side surface for the DataProcessing
// protocol. Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Protocol surfaces / DataProcessing.
//
// @concept: data-processing
// @concept: claim-tree
// @concept: fan-out
//
// The DataProcessing protocol is the opt-in producer-side mix-in that
// supplies per-sub-claim BeginCandidate / CommitCandidate /
// AbandonCandidate verbs alongside the standard ClaimProducer surface.
// Bundles like `stub/dataprocessing` and the openlineage-emitting
// producers advertise it; rimsky talks to them via the gRPC remote
// client in `runtime/remote/data_processing_client.go`.
//
// The runtime-side wrapper here lets the supervisor's fan-out path
// dial the producer's candidate verbs without leaking the proto-gen
// dependency across runtime files. Test fixtures satisfy the same
// interface so the fan-out unit tests can exercise BeginCandidate
// without the gRPC wire.

package runtime

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
