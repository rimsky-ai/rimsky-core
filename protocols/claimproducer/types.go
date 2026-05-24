// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import "encoding/json"

// WriteSemantics declares how a claim handle's writes coexist with
// concurrent claims on byte-equal scopes. See spec §2.4.
//
// Per Phase 4 of the layer-crystallization design (2026-05-04), the
// previously single-valued capability is replaced by an envelope: a
// ClaimProducer advertises a SET of permissible WriteSemantics values
// via Capabilities, and Open returns the realized value per claim. The
// producer is required to honor the uniformity invariant — two claims
// with byte-equal Scope MUST yield identical RealizedWriteSemantics.
type WriteSemantics string

// WriteSemantics values.
const (
	// WriteSemanticsUnknown is the proto-default zero value; producers
	// that return Unknown are malformed and the supervisor must reject
	// the claim result.
	WriteSemanticsUnknown WriteSemantics = ""

	// WriteSemanticsSync — synchronous in-place writes; r×rw on
	// byte-equal scopes block.
	WriteSemanticsSync WriteSemantics = "sync"

	// WriteSemanticsStagedAsync — writes go to a staging area; reads
	// see a stable snapshot during writes; r×rw on byte-equal scopes
	// does NOT block.
	WriteSemanticsStagedAsync WriteSemantics = "staged_async"

	// WriteSemanticsBlockingAsync — writes go to a staging area;
	// r×rw on byte-equal scopes block until commit/abandon.
	WriteSemanticsBlockingAsync WriteSemantics = "blocking_async"

	// WriteSemanticsReadOnly — claim cannot mutate; useful for pure-read
	// producers.
	WriteSemanticsReadOnly WriteSemantics = "read_only"
)

// String returns a stable lowercase spelling for logs/config.
func (w WriteSemantics) String() string { return string(w) }

// ParseWriteSemantics maps the YAML/JSON spelling back to the constant.
// Unknown spellings return WriteSemanticsUnknown and ok=false.
func ParseWriteSemantics(s string) (WriteSemantics, bool) {
	switch s {
	case string(WriteSemanticsSync):
		return WriteSemanticsSync, true
	case string(WriteSemanticsStagedAsync):
		return WriteSemanticsStagedAsync, true
	case string(WriteSemanticsBlockingAsync):
		return WriteSemanticsBlockingAsync, true
	case string(WriteSemanticsReadOnly):
		return WriteSemanticsReadOnly, true
	default:
		return WriteSemanticsUnknown, false
	}
}

// ClaimID is the rimsky-generated UUID (textual form) that identifies a
// single claim across every protocol verb in its lifecycle. Generated
// client-side immediately before Open; threaded through every subsequent
// verb (Commit / Abandon / Release).
type ClaimID string

// Intent is the graph author's declaration of what the executor will do
// with the claim. Stored on each scope-kind lock-holder row and consumed
// by the supervisor's mode-coexistence check.
type Intent string

// Intent values.
const (
	IntentRead      Intent = "r"
	IntentReadWrite Intent = "rw"
)

// ClaimSpec is the producer-bound claim primitive. Callers build one
// per acquisition; producers parse Selector and decide what it means
// (scoped access vs. configured pick policy).
type ClaimSpec struct {
	ProducerName string // operator-configured producer name
	Selector     string // opaque text (post-substitution); producer parses
	Intent       Intent // "r" | "rw"
	Alias        string // per-claim name within node; defaults to ProducerName
	TemplateID   string // content hash (template-scope envelope)
	InstanceID   string // instance UUID (instance-scope envelope)
}

// ClaimResult bundles the four producer-supplied outputs of a claim
// acquisition. Address, Payload, and ClaimScope are inert in Rimsky
// (foundation invariant 20): rimsky reads them only at substitution-leaf
// extraction. RealizedWriteSemantics declares the per-claim semantics;
// must be a member of the producer's Capabilities.WriteSemanticsEnvelope;
// must be uniform across byte-equal-claim-scope claims (uniformity invariant
// per spec §2.5).
type ClaimResult struct {
	Address                json.RawMessage // producer-supplied pointer the executor uses
	Payload                json.RawMessage // producer-supplied data captured at acquisition
	ClaimScope             json.RawMessage // canonicalized claim-scope bytes
	RealizedWriteSemantics WriteSemantics
}

// OpenOutcome mirrors the OpenResponse oneof on the wire.
// Available == true means the producer returned Acquired{...};
// Available == false means Unavailable{}. Result is populated only
// when Available is true; its fields remain opaque json.RawMessage
// bytes per blessed invariant 20.
type OpenOutcome struct {
	Available bool
	Result    ClaimResult
}

// Capabilities describes what a ClaimProducer advertises.
//
// WriteSemanticsAllowed is the SET of permissible WriteSemantics values
// the producer may realize on Open. Operator config (rimsky.yml's
// claim_producers[*].write_semantics_allowed) declares a permitted
// subset that MUST be a subset of the advertised set; rimsky validates
// strict subset at startup.
//
// SupportsSplitScope and SupportsScopesConflict declare whether the
// producer implements the optional partitioning RPCs (see
// proto:claim_producer.proto::ClaimProducer.SplitScope and
// ClaimProducer.ScopesConflict). When SupportsScopesConflict is false
// rimsky uses byte-equal scope conflict as the trivial default per
// @blessed-invariant 4b.
//
// Protocols lists mix-in service-protocol names the binary implements
// alongside ClaimProducer (e.g. "data_processing", "validation",
// "lifecycle_subscriber"). ValidationSupportedRoles is set when the
// "validation" mix-in is advertised; lists the role discriminators the
// service is willing to validate ("executor" | "claim_producer" |
// "lifecycle_subscriber" | "sensor").
type Capabilities struct {
	WriteSemanticsAllowed    []WriteSemantics
	SupportsSplitScope       bool
	SupportsScopesConflict   bool
	Protocols                []string
	ValidationSupportedRoles []string
}

// Contains reports whether the advertised allowed set includes w.
func (c Capabilities) Contains(w WriteSemantics) bool {
	for _, v := range c.WriteSemanticsAllowed {
		if v == w {
			return true
		}
	}
	return false
}

// AdvertisesProtocol reports whether the producer advertises the named
// mix-in protocol (e.g. "data_processing", "validation").
func (c Capabilities) AdvertisesProtocol(p string) bool {
	for _, v := range c.Protocols {
		if v == p {
			return true
		}
	}
	return false
}

// SplitClaimScopeRequest is the rimsky-side input to ClaimProducer.SplitClaimScope.
// The parent claim handle MUST already be Open'd. partition_request is
// producer-interpreted opaque bytes. Per spec §Fan-out template DSL.
type SplitClaimScopeRequest struct {
	ClaimHandleID    string
	PartitionRequest []byte
}

// SubClaimScopeDescriptor identifies one of the sub-claim-scopes the producer
// partitioned the parent into. ClaimScopeData is producer-canonicalized
// bytes (same shape rimsky stores on rimsky_claim_handles.claim_scope_data);
// PartitionKey is the human-readable key rimsky persists in
// col:rimsky_node_runs.child_key for run-tree bookkeeping;
// ProducerMetadata is opaque per-sub-claim-scope info the producer wants
// persisted on the row.
type SubClaimScopeDescriptor struct {
	ClaimScopeData   []byte
	PartitionKey     string
	ProducerMetadata []byte
}

// SplitClaimScopeResponse is the producer's reply to SplitClaimScope.
type SplitClaimScopeResponse struct {
	SubClaimScopes []SubClaimScopeDescriptor
}

// Mix-in protocol names producers may advertise in
// Capabilities.Protocols. Constants live here so callers compare
// against a single source of truth.
const (
	ProtocolDataProcessing      = "data_processing"
	ProtocolValidation          = "validation"
	ProtocolLifecycleSubscriber = "lifecycle_subscriber"
)
