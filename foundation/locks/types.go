// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Value types for the ClaimProducer interface (spec §2.4, §2.5, §2.6).
//
// Two primitives split (spec §2.1 / glossary):
//
//   - Claim — producer-bound; ClaimSpec carries (ProducerName, Selector,
//     Intent, Alias). The producer parses Selector and decides what it
//     means (scoped access vs. configured pick policy).
//
//   - Named lock — producer-independent; NamedLockSpec carries (Name)
//     only. Limit lives in operator config.
//
// Two types, no common interface: ClaimSpec and NamedLockSpec are
// distinct. Callers dispatch by type.
//
// Per the layer-crystallization design (2026-05-04), the canonical Go
// surface for the ClaimProducer protocol lives in
// github.com/fallguy/rimsky/protocols/claimproducer. The types declared
// here are Go type aliases (not duplicate declarations) so a value
// satisfying protocols/claimproducer.X is interchangeable with
// foundation/locks.X. External authors should import
// protocols/claimproducer; rimsky-internal code may use either.
//
// Constants cannot be aliased in Go, so the const block re-declares the
// WriteSemantics and Intent values using the aliased types. The values
// equal their protocols/claimproducer counterparts by string value;
// (==) comparisons across the two packages work naturally.

package locks

import (
	"github.com/fallguy/rimsky/protocols/claimproducer"
)

// ClaimID is the rimsky-generated UUID (textual form) that identifies a
// single claim across every protocol verb in its lifecycle. Generated
// client-side immediately before Open; persisted in
// rimsky_claim_handles.id; passed unchanged to Commit / Abandon /
// Release.
type ClaimID = claimproducer.ClaimID

// Intent is the graph author's declaration of what the executor will do
// with the claim. Stored on each scope-kind lock-holder row and consumed
// by the supervisor's mode-coexistence check.
type Intent = claimproducer.Intent

// Intent values — re-declared here because Go does not allow constant
// aliasing. Values equal the protocols/claimproducer counterparts.
const (
	IntentRead      = claimproducer.IntentRead
	IntentReadWrite = claimproducer.IntentReadWrite
)

// ClaimSpec is the producer-bound claim primitive.
//
// Acquisition is the only thing the spec carries: rimsky tells the
// producer which producer-name, which selector, which intent, and the
// template-side alias (opaque to the producer). Disposition at
// terminal — what Commit / Abandon mean for the producer's own state
// (publish staging, delete an items-table row, release-to-back, etc.) —
// is governed entirely by per-producer config. Rimsky carries only
// the success/failure binary; the producer decides the rest.
//
// TemplateID and InstanceID carry the per-spec scope envelope: opaque
// strings rimsky never inspects, populated from the dispatch row's
// instance → template lookup, sent to the producer on Open for namespace
// routing or trace correlation.
type ClaimSpec = claimproducer.ClaimSpec

// NamedLockSpec is the producer-independent named-lock primitive.
// Templates reference named locks by name only; the limit (mutex vs.
// counting) lives in the operator's named_locks: config block.
//
// NamedLockSpec is rimsky-internal — it has no protocol-layer
// equivalent because named locks never cross the wire to a producer.
type NamedLockSpec struct {
	Name string
}

// ClaimResult bundles the four producer-supplied outputs of a claim
// acquisition. Address, Payload, and Scope are opaque-bytes from rimsky's
// perspective; the substitution engine extracts named-field paths only at
// the leaf extraction site (graph/attribute/substitution.go::walkPath).
//
// RealizedWriteSemantics declares the per-claim semantics; MUST be a
// member of the producer's Capabilities.WriteSemanticsAllowed; MUST be
// uniform across byte-equal-Scope claims (uniformity invariant per spec
// §2.5).
//
// @blessed-invariant 20: claim content is inert in rimsky.
//
//	Address, Payload, Scope are producer-supplied opaque bytes.
//	Rimsky reads them by named-field path only at substitution-leaf
//	extraction (graph/attribute/substitution.go::walkPath); does not
//	log, validate, transform, normalize, decrypt, hash, index,
//	pattern-match, attach to traces, include in errors, or otherwise
//	act on the content. Distinct from store-config bytes (operator-
//	managed; not under invariant 20).
type ClaimResult = claimproducer.ClaimResult

// OpenOutcome is the rimsky-side discriminator that mirrors the
// OpenResponse oneof on the wire. Available == true means the
// producer returned Acquired{...}; Available == false means
// Unavailable{}. Result is populated only when Available is true; its
// fields remain opaque json.RawMessage bytes per blessed invariant 20.
type OpenOutcome = claimproducer.OpenOutcome

// WriteSemantics declares how a claim's writes coexist with concurrent
// claims on byte-equal scopes. Per Phase 4 of the layer-crystallization
// design (2026-05-04) the previously single-valued capability is replaced
// by an envelope: a ClaimProducer advertises a SET of permissible values
// via Capabilities, and Open returns the realized value per claim.
type WriteSemantics = claimproducer.WriteSemantics

// WriteSemantics values — re-declared here because Go does not allow
// constant aliasing. Values equal the protocols/claimproducer
// counterparts (compared by string).
const (
	// WriteSemanticsUnknown is the proto-default zero value; producers
	// that return Unknown are malformed and the supervisor must reject
	// the claim result.
	WriteSemanticsUnknown = claimproducer.WriteSemanticsUnknown

	// WriteSemanticsSync — synchronous in-place writes; r×rw on
	// byte-equal scopes block.
	WriteSemanticsSync = claimproducer.WriteSemanticsSync

	// WriteSemanticsStagedAsync — writes go to a staging area; reads
	// see a stable snapshot during writes; r×rw on byte-equal scopes
	// does NOT block.
	WriteSemanticsStagedAsync = claimproducer.WriteSemanticsStagedAsync

	// WriteSemanticsBlockingAsync — writes go to a staging area;
	// r×rw on byte-equal scopes block until commit/abandon.
	WriteSemanticsBlockingAsync = claimproducer.WriteSemanticsBlockingAsync

	// WriteSemanticsReadOnly — claim cannot mutate; useful for pure-read
	// producers.
	WriteSemanticsReadOnly = claimproducer.WriteSemanticsReadOnly
)

// ParseWriteSemantics maps the YAML/JSON spelling back to the constant.
// Unknown spellings return WriteSemanticsUnknown and ok=false.
//
// Delegates to claimproducer.ParseWriteSemantics so both packages stay
// in lockstep.
func ParseWriteSemantics(s string) (WriteSemantics, bool) {
	return claimproducer.ParseWriteSemantics(s)
}

// Capabilities describes what a ClaimProducer advertises.
//
// WriteSemanticsAllowed is a SET of permissible WriteSemantics values
// the producer may realize on Open. Operator config (rimsky.yml's
// claim_producers[*].write_semantics_allowed) declares an envelope that
// MUST be a subset of the advertised set; rimsky validates strict subset
// at startup.
type Capabilities = claimproducer.Capabilities
