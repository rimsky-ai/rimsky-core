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
// acquisition. Address, Payload, and Scope are inert in Rimsky
// (foundation invariant 20): rimsky reads them only at substitution-leaf
// extraction. RealizedWriteSemantics declares the per-claim semantics;
// must be a member of the producer's Capabilities.WriteSemanticsEnvelope;
// must be uniform across byte-equal-scope claims (uniformity invariant
// per spec §2.5).
type ClaimResult struct {
	Address                json.RawMessage // producer-supplied pointer the executor uses
	Payload                json.RawMessage // producer-supplied data captured at acquisition
	Scope                  json.RawMessage // canonicalized scope bytes
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
type Capabilities struct {
	WriteSemanticsAllowed []WriteSemantics
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
