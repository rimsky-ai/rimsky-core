// Value types for the Store interface (spec §4.2, §4.6, §4.7, §4.8).
//
// Two primitives split (spec §3 / glossary):
//
//   - Claim — substrate-bound; ClaimSpec carries (StoreName, Selector,
//     Intent, Alias). The substrate parses Selector and decides what it
//     means (regional access vs. configured pick policy).
//
//   - Named lock — non-substrate; NamedLockSpec carries (Name) only.
//     Limit lives in operator config.
//
// Two types, no common interface: ClaimSpec and NamedLockSpec are
// distinct. Callers dispatch by type.

package store

import "encoding/json"

// ClaimID is the rimsky-generated UUID (textual form) that identifies a
// single claim across every protocol verb in its lifecycle. Generated
// client-side immediately before Open; persisted in
// rimsky_lock_holders.id; passed unchanged to Commit / Abandon /
// Release. Per spec §4.2.
type ClaimID string

// Intent is the graph author's declaration of what the executor will do
// with the claim. Stored on each region-kind lock-holder row and consumed
// by the supervisor's mode-coexistence check.
type Intent string

// Intent values.
const (
	IntentRead      Intent = "r"
	IntentReadWrite Intent = "rw"
)

// ClaimSpec is the substrate-bound claim primitive (spec §4.6).
//
// Acquisition is the only thing the spec carries: rimsky tells the
// substrate which store, which selector, which intent, and the
// template-side alias (opaque to the substrate). Disposition at
// terminal — what Commit / Abandon mean for the substrate's own state
// (publish staging, delete an items-table row, release-to-back, etc.) —
// is governed entirely by per-substrate config. Rimsky carries only
// the success/failure binary; the substrate decides the rest. Per the
// 2026-04-30 cleanup amending v3 §4.6.
type ClaimSpec struct {
	StoreName string // operator-configured store name
	Selector  string // opaque text (post-substitution); substrate parses
	Intent    Intent // "r" | "rw"
	Alias     string // per-claim name within node; defaults to StoreName
}

// NamedLockSpec is the non-substrate named-lock primitive. Templates
// reference named locks by name only; the limit (mutex vs. counting)
// lives in the operator's named_locks: config block.
type NamedLockSpec struct {
	Name string
}

// ClaimResult bundles the three substrate-supplied outputs of a claim
// acquisition. All three are opaque-bytes from rimsky's perspective;
// the substitution engine extracts named-field paths only at the leaf
// extraction site (core/attributes/substitution.go::walkPath).
//
// @blessed-invariant 20: claim content is inert in rimsky.
//
//	Address, Payload, Region are substrate-supplied opaque bytes.
//	Rimsky reads them by named-field path only at substitution-leaf
//	extraction (core/attributes/substitution.go::walkPath); does not
//	log, validate, transform, normalize, decrypt, hash, index,
//	pattern-match, attach to traces, include in errors, or otherwise
//	act on the content. Distinct from store-config bytes (operator-
//	managed; not under invariant 20 — see v3 spec §13.3).
type ClaimResult struct {
	Address json.RawMessage // substrate-native pointer the executor uses
	Payload json.RawMessage // substrate-supplied data captured at acquisition
	Region  json.RawMessage // substrate's identifier for the claimed region
}

// OpenOutcome is the rimsky-side discriminator that mirrors the
// OpenResponse oneof on the wire. Available == true means the
// substrate returned Acquired{...}; Available == false means
// Unavailable{}. Result is populated only when Available is true; its
// fields remain opaque json.RawMessage bytes per blessed invariant 20.
type OpenOutcome struct {
	Available bool
	Result    ClaimResult
}

// WriteSemantics declares how a store coordinates writes with readers.
// One of three values; per-store; baked into the store-service's own
// config and reported via Capabilities. Strict equality between operator-
// declared and store-advertised values per spec §4.8 / §6.2.
type WriteSemantics string

// WriteSemantics values.
const (
	// WriteSemanticsDirect: writes hit live data; no staging area.
	// r×rw on overlapping regions blocks (sync semantics).
	WriteSemanticsDirect WriteSemantics = "direct"

	// WriteSemanticsStagedBlocking: writes go to a substrate-private
	// staging area; Commit does atomic swap into live; Abandon
	// discards staging. r×rw on overlapping regions blocks (sync
	// semantics).
	WriteSemanticsStagedBlocking WriteSemantics = "staged_blocking"

	// WriteSemanticsStagedAsync: writes go to a staging area; reads
	// see a stable view of live state during writes. r×rw on
	// overlapping regions does NOT block (async semantics).
	WriteSemanticsStagedAsync WriteSemantics = "staged_async"
)

// Capabilities describes what a store advertises. Single-field shape
// in v3; future capabilities can be added as struct fields without
// breaking the interface.
type Capabilities struct {
	WriteSemantics WriteSemantics
}
