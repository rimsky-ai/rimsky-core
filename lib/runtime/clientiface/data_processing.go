// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package clientiface

import "context"

type DataProcessingClient interface {
	Name() string

	BeginCandidate(ctx context.Context, in BeginCandidateInput) (BeginCandidateOutput, error)

	CommitCandidate(ctx context.Context, in CommitCandidateInput) (CommitCandidateOutput, error)

	AbandonCandidate(ctx context.Context, in AbandonCandidateInput) error

	ListVersions(ctx context.Context, in ListVersionsInput) (ListVersionsOutput, error)

	ListPartitions(ctx context.Context, in ListPartitionsInput) (ListPartitionsOutput, error)

	GetVersionSchema(ctx context.Context, in GetVersionSchemaInput) (GetVersionSchemaOutput, error)
}

type DataProcessingRegistry interface {
	Get(name string) (DataProcessingClient, bool)
}

type BeginCandidateInput struct {
	ProducerName       string
	ClaimHandleID      string
	SubScopeDescriptor []byte
	IdempotencyKey     string
}

type BeginCandidateOutput struct {
	CandidateHandle []byte
}

type CommitCandidateInput struct {
	ProducerName    string
	ClaimHandleID   string
	CandidateHandle []byte
}

type CommitCandidateOutput struct {
	VersionID         string
	CandidateMetadata []byte
}

type AbandonCandidateInput struct {
	ProducerName    string
	ClaimHandleID   string
	CandidateHandle []byte
}

type ListVersionsInput struct {
	ProducerName  string
	ClaimHandleID string
}

type ListVersionsOutput struct {
	Versions []DataProcessingVersion
}

type DataProcessingVersion struct {
	VersionID        string
	CommittedAtUnixS int64
	ProducerMetadata []byte
}

type ListPartitionsInput struct {
	ProducerName  string
	ClaimHandleID string
	VersionID     string
}

type ListPartitionsOutput struct {
	Partitions []DataProcessingPartition
}

type DataProcessingPartition struct {
	PartitionKey      string
	PartitionMetadata []byte
}

type GetVersionSchemaInput struct {
	ProducerName  string
	ClaimHandleID string
	VersionID     string
}

type GetVersionSchemaOutput struct {
	Schema []byte
}
