// Value types for the ClaimProducer interface (spec §2.4, §2.5, §2.6).
//
// Two primitives split (spec §2.1 / glossary):
//
//   - Claim — producer-bound; ClaimSpec carries (StoreName, Selector,
//     Intent, Alias). The producer parses Selector and decides what it
//     means (scoped access vs. configured pick policy).
//
//   - Named lock — producer-independent; NamedLockSpec carries (Name)
//     only. Limit lives in operator config.
//
// Two types, no common interface: ClaimSpec and NamedLockSpec are
// distinct. Callers dispatch by type.

package locks

import "encoding/json"

// ClaimID is the rimsky-generated UUID (textual form) that identifies a
// single claim across every protocol verb in its lifecycle. Generated
// client-side immediately before Open; persisted in
// rimsky_lock_holders.id; passed unchanged to Commit / Abandon /
// Release.
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
type ClaimSpec struct {
	StoreName  string // operator-configured producer name
	Selector   string // opaque text (post-substitution); producer parses
	Intent     Intent // "r" | "rw"
	Alias      string // per-claim name within node; defaults to StoreName
	TemplateID string // content hash (template-scope envelope)
	InstanceID string // instance UUID (instance-scope envelope)
}

// NamedLockSpec is the producer-independent named-lock primitive.
// Templates reference named locks by name only; the limit (mutex vs.
// counting) lives in the operator's named_locks: config block.
type NamedLockSpec struct {
	Name string
}

// ClaimResult bundles the four producer-supplied outputs of a claim
// acquisition. Address, Payload, and Scope are opaque-bytes from rimsky's
// perspective; the substitution engine extracts named-field paths only at
// the leaf extraction site (modeling/attribute/substitution.go::walkPath).
//
// RealizedWriteSemantics declares the per-claim semantics; MUST be a
// member of the producer's Capabilities.WriteSemanticsEnvelope; MUST be
// uniform across byte-equal-Scope claims (uniformity invariant per spec
// §2.5).
//
// @blessed-invariant 20: claim content is inert in rimsky.
//
//	Address, Payload, Scope are producer-supplied opaque bytes.
//	Rimsky reads them by named-field path only at substitution-leaf
//	extraction (modeling/attribute/substitution.go::walkPath); does not
//	log, validate, transform, normalize, decrypt, hash, index,
//	pattern-match, attach to traces, include in errors, or otherwise
//	act on the content. Distinct from store-config bytes (operator-
//	managed; not under invariant 20).
type ClaimResult struct {
	Address                json.RawMessage // producer-supplied pointer the executor uses
	Payload                json.RawMessage // producer-supplied data captured at acquisition
	Scope                  json.RawMessage // the producer's identifier for the claimed scope
	RealizedWriteSemantics WriteSemantics  // per-claim semantics; must be in envelope
}

// OpenOutcome is the rimsky-side discriminator that mirrors the
// OpenResponse oneof on the wire. Available == true means the
// producer returned Acquired{...}; Available == false means
// Unavailable{}. Result is populated only when Available is true; its
// fields remain opaque json.RawMessage bytes per blessed invariant 20.
type OpenOutcome struct {
	Available bool
	Result    ClaimResult
}

// WriteSemantics declares how a claim's writes coexist with concurrent
// claims on byte-equal scopes. Per Phase 4 of the layer-crystallization
// design (2026-05-04) the previously single-valued capability is replaced
// by an envelope: a ClaimProducer advertises a SET of permissible values
// via Capabilities, and Open returns the realized value per claim.
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

// Capabilities describes what a ClaimProducer advertises.
//
// WriteSemanticsEnvelope is a SET of permissible WriteSemantics values
// the producer may realize on Open. Operator config (rimsky.yml's
// claim_producers[*].write_semantics_envelope) declares an envelope that
// MUST be a subset of the advertised set; rimsky validates strict subset
// at startup.
type Capabilities struct {
	WriteSemanticsEnvelope []WriteSemantics
}

// Contains reports whether the Capabilities advertised envelope includes
// the given WriteSemantics value.
func (c Capabilities) Contains(w WriteSemantics) bool {
	for _, v := range c.WriteSemanticsEnvelope {
		if v == w {
			return true
		}
	}
	return false
}
