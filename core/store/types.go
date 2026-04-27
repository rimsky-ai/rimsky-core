// Value types for the Store interface (spec §11.3 / §11.4).
//
// Two primitives split (spec §5.1, glossary):
//
//   - Claim — substrate-bound; ClaimSpec carries (StoreName, Selector,
//     Intent, Alias). The substrate parses Selector and decides what it
//     means (regional access vs. configured pick policy).
//
//   - Named lock — non-substrate; NamedLockSpec carries (Name) only.
//     Limit lives in operator config (§15.2).
//
// Two types, no common interface: ClaimSpec and NamedLockSpec are
// distinct. Callers dispatch by type.
//
// See also: docs/glossary.md.

package store

import "encoding/json"

// Intent is the graph author's declaration of what the executor will do
// with the claim. Stored on each region-kind lock-holder row and consumed
// by the supervisor's mode-coexistence check (§8.5).
type Intent string

// Intent values.
const (
	IntentRead      Intent = "r"
	IntentReadWrite Intent = "rw"
)

// ClaimSpec is the substrate-bound claim primitive (spec §11.3).
//
// There is no PolicyOverride field on ClaimSpec. Substrate-internal action
// vocabulary (e.g. delete / release_to_back / release_to_head for pick
// policies) is plumbed at terminal time via the policyOverride argument on
// Commit / Abandon — not at acquisition. Per-claim resolution actions are
// declared on the acquiring node's claim_resolutions block (§14.3) and
// passed to the verbs at terminal by the supervisor.
type ClaimSpec struct {
	StoreName string // operator-configured store name
	Selector  string // opaque text (post-substitution); substrate parses
	Intent    Intent // "r" | "rw"
	Alias     string // per-claim name within node; defaults to StoreName
}

// NamedLockSpec is the non-substrate named-lock primitive (spec §11.3).
// Templates reference named locks by name only; the limit (mutex vs.
// counting) lives in the operator's named_locks: config block (§15.2).
type NamedLockSpec struct {
	Name string
}

// ClaimResult bundles the three substrate-supplied outputs of a claim
// acquisition. All three are opaque-bytes from Rimsky's perspective; the
// substitution engine extracts named-field paths only at the leaf
// extraction site (core/attributes/substitution.go::walkPath).
//
// @blessed-invariant 20: claim content is inert in Rimsky.
//
//	Address, Payload, Region are substrate-supplied opaque bytes.
//	Rimsky reads them by named-field path only at substitution-leaf
//	extraction (core/attributes/substitution.go::walkPath); does not
//	log, validate, transform, normalize, decrypt, hash, index,
//	pattern-match, attach to traces, include in errors, or otherwise
//	act on the content. Distinct from store-config bytes (operator-
//	managed; not under invariant 20 — see spec §17.5).
type ClaimResult struct {
	Address json.RawMessage // substrate-native pointer the executor uses
	Payload json.RawMessage // substrate-supplied data captured at acquisition
	Region  json.RawMessage // substrate's identifier for the claimed region
}

// WriteSemantics declares how a store coordinates writes with readers
// (spec §8). One of three values; per-store, operator-configured, bounded
// above by the store kind's max capability.
type WriteSemantics string

// WriteSemantics values.
const (
	// WriteSemanticsDirect: writes hit live data; no staging area.
	// r×rw on overlapping regions blocks (sync semantics).
	WriteSemanticsDirect WriteSemantics = "direct"

	// WriteSemanticsStagedBlocking: writes go to a substrate-private
	// staging area; Commit does atomic swap into live; Abandon discards
	// staging. r×rw on overlapping regions blocks (sync semantics).
	WriteSemanticsStagedBlocking WriteSemantics = "staged_blocking"

	// WriteSemanticsStagedAsync: writes go to a staging area; reads see a
	// stable view of live state during writes (substrate-native MVCC or
	// snapshot delegation). r×rw on overlapping regions does NOT block
	// (async semantics). No v1 substrate exercises this; the protocol
	// supports it for future use.
	WriteSemanticsStagedAsync WriteSemantics = "staged_async"
)

// Capabilities describes what a Store implementation supports. Exactly one
// field in v1 (spec §9). Future capabilities can be added as struct
// fields without breaking the interface.
type Capabilities struct {
	WriteSemantics WriteSemantics
}
