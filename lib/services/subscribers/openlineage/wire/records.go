// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage
// @concept: lineage-record
package wire

type LeafRunRecord struct {
	RunID              string            `json:"run_id"`
	NodeID             string            `json:"node_id"`
	FrameID            string            `json:"frame_id"`
	ChildKey           string            `json:"child_key,omitempty"`
	NodeAlias          string            `json:"node_alias,omitempty"`
	ParentRunID        string            `json:"parent_run_id,omitempty"`
	FrameTriggerKind   string            `json:"frame_trigger_kind,omitempty"`
	TriggerMessageID   string            `json:"trigger_message_id,omitempty"`
	HeldClaims         []HeldClaimRef    `json:"held_claims,omitempty"`
	ExecutorName       string            `json:"executor_name,omitempty"`
	TemplateHash       string            `json:"template_hash,omitempty"`
	TemplateNodeAlias  string            `json:"template_node_alias,omitempty"`
	ParamsSnapshotHash string            `json:"params_snapshot_hash,omitempty"`
	AttributesHash     string            `json:"attributes_hash,omitempty"`
	ScopeDataHash      string            `json:"claim_scope_data_hash,omitempty"`
	State              string            `json:"state"`
	SettlingSignalType string            `json:"settling_signal_type"`
	Changed            bool              `json:"changed,omitempty"`
	TerminalKind       string            `json:"terminal_kind,omitempty"`
	ErrorClass         string            `json:"error_class,omitempty"`
	SubstitutionRefs   []SubstitutionRef `json:"substitution_refs,omitempty"`
	Extra              map[string]any    `json:"extra,omitempty"`
}

type HeldClaimRef struct {
	ClaimHandleID string `json:"claim_handle_id"`
	Role          string `json:"role"`
	ProducerName  string `json:"producer_name"`
	ScopeDataHash string `json:"claim_scope_data_hash"`
}

type SubstitutionRef struct {
	SourceKind        string `json:"source_kind"`
	SourceNodeAlias   string `json:"source_node_alias,omitempty"`
	SourceVersionOrID string `json:"source_version_or_id,omitempty"`
}

type ClaimTerminalRecord struct {
	ClaimHandleID       string         `json:"claim_handle_id"`
	RunID               string         `json:"run_id"`
	NodeID              string         `json:"node_id"`
	FrameID             string         `json:"frame_id"`
	ParentClaimHandleID string         `json:"parent_claim_handle_id,omitempty"`
	OpenLineageRunRef   string         `json:"open_lineage_run_ref,omitempty"`
	SubClaimHandleIDs   []string       `json:"sub_claim_handle_ids,omitempty"`
	CommittedAt         string         `json:"committed_at,omitempty"`
	ProducerName        string         `json:"producer_name,omitempty"`
	ScopeDataHash       string         `json:"claim_scope_data_hash,omitempty"`
	VersionID           string         `json:"version_id,omitempty"`
	Outcome             string         `json:"outcome"`
	Cause               string         `json:"cause,omitempty"`
	ProducerMetadata    map[string]any `json:"producer_metadata,omitempty"`
	// @concept: lineage
	TerminatingSupervisorID string `json:"terminating_supervisor_id,omitempty"`
}
