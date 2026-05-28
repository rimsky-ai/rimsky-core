// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// data_processing.go — runtime-side surface for the DataProcessing
// protocol. The canonical interface + DTO types live in
// `runtime/clientiface/` (Apache-licensed) so the wire-surface gRPC
// remote client in `runtime/peer/data_processing_client.go` can
// implement them without crossing the licensing boundary; this file
// re-exports them under the `runtime` package via type aliases so
// every other AGPL-licensed runtime file continues to refer to them
// by their unqualified names.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Protocol surfaces / DataProcessing.
//
// @concept: data-processing
// @concept: claim-tree
// @concept: fan-out

package runtime

import "github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"

// DataProcessingClient is the rimsky-side wrapper around a producer's
// DataProcessing gRPC client. See `clientiface.DataProcessingClient`
// for the full doc.
type DataProcessingClient = clientiface.DataProcessingClient

// DataProcessingRegistry resolves a producer name to the matching
// DataProcessingClient. See `clientiface.DataProcessingRegistry`.
type DataProcessingRegistry = clientiface.DataProcessingRegistry

// BeginCandidateInput / Output and friends are the rimsky-side
// payloads for the DataProcessing verbs; see `clientiface.*` for the
// canonical doc.
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
