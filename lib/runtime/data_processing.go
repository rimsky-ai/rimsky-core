// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
