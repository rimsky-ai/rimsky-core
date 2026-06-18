// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: data-processing
// @concept: claim-tree
// @concept: fan-out

package runtime

import "github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"

type DataProcessingClient = clientiface.DataProcessingClient

type DataProcessingRegistry = clientiface.DataProcessingRegistry

type (
	BeginCandidateInput     = clientiface.BeginCandidateInput
	BeginCandidateOutput    = clientiface.BeginCandidateOutput
	CommitCandidateInput    = clientiface.CommitCandidateInput
	CommitCandidateOutput   = clientiface.CommitCandidateOutput
	AbandonCandidateInput   = clientiface.AbandonCandidateInput
	ListVersionsInput       = clientiface.ListVersionsInput
	ListVersionsOutput      = clientiface.ListVersionsOutput
	DataProcessingVersion   = clientiface.DataProcessingVersion
	ListPartitionsInput     = clientiface.ListPartitionsInput
	ListPartitionsOutput    = clientiface.ListPartitionsOutput
	DataProcessingPartition = clientiface.DataProcessingPartition
	GetVersionSchemaInput   = clientiface.GetVersionSchemaInput
	GetVersionSchemaOutput  = clientiface.GetVersionSchemaOutput
)
