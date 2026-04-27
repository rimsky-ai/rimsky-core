// Store interface (spec §11.5). Five protocol verbs.
//
// One protocol surface for both regional access and substrate-side pick
// policies. Selectors are opaque text; substrates parse them and decide
// what they mean. There is no claim_store kind; pick policies are
// configured per-store via the substrate's pick_policies block.
//
// See also: docs/glossary.md, docs/specs/2026-04-27-stores-redesign-v2-design.md.

package store

import "context"

// Store is the universal interface every store implementation must satisfy.
//
// @blessed-invariant 9a: Lock state lives only in postgres.
//
//	No Store implementation persists lock state. Stores may persist *data*
//	state (e.g. items-table flips, staging-area metadata), but the
//	question "is anyone holding lock X" is answered exclusively by
//	rimsky_lock_holders. (Spec §21 invariant 9a.) A scenario test
//	exercises this invariant; do not add store-side lock-state caches
//	that would violate it.
//
// @blessed-invariant 9b: Store implementations do not internally serialize
// on lock-shaped predicates.
//
//	The §9-strategy-2 reader-lease serialization pattern (substrate
//	tracks active read leases; writers block at the substrate boundary)
//	is not a valid implementation choice for staged_async. Honest support
//	requires snapshot delegation or native MVCC pass-through. A substrate
//	that cannot honestly provide stable reads during writes declares
//	staged_blocking (or direct). (Spec §21 invariant 9b.)
type Store interface {
	// Kind returns the canonical store kind, e.g. "filesystem" |
	// "postgres" | "stub_filesystem" | "stub_postgres".
	Kind() string

	// Name returns the operator-configured store name; matches stores.<name>
	// in YAML.
	Name() string

	// Capabilities reports what this store supports.
	Capabilities() Capabilities

	// RegionsConflict is the region-overlap predicate; called by the
	// supervisor (and the queue eligibility predicate) when comparing a
	// candidate region acquisition against an existing holder for this
	// store. Returns true if the two regions conflict (cannot both be
	// held at once when modes also conflict).
	//
	// @blessed-invariant 14: RegionsConflict and UnmarshalRegion are pure.
	//
	//	No side effects, no external state read; deterministic on inputs.
	//	(Spec §21 invariant 14.) The supervisor calls these inside the
	//	atomic acquisition transaction (§13.3) and inside hot eligibility
	//	loops; impurity here would corrupt acquisition correctness.
	RegionsConflict(a, b []byte) bool

	// UnmarshalRegion deserializes region_data JSONB into a canonical
	// byte form for use with RegionsConflict. The supervisor calls this
	// on each existing-holder row before passing to RegionsConflict.
	//
	// @blessed-invariant 14: same purity contract as RegionsConflict.
	UnmarshalRegion(raw []byte) ([]byte, error)

	// Open produces a substrate-native address for the executor and
	// registers whatever substrate-side state the (intent, write_semantics)
	// combination requires (staging area, snapshot, MVCC transaction, or
	// nothing).
	//
	// Inside the supervisor's atomic acquisition transaction (§13.3); the
	// supervisor passes its open *pgx.Tx via ctx (key store.txKey,
	// accessed through TxFromContext) so substrate writes participate in
	// the same transaction.
	//
	// Substrate detects resumed-vs-fresh by lookup against its own state
	// keyed by lock-holder identity. There is no resumed flag; resume is
	// universal.
	//
	// For pick-policy claims (selectors the substrate recognizes as
	// policy-form), Open invokes the configured pick policy and returns
	// the picked item's address; the picked identifier becomes the
	// region_data on the lock-holder row.
	//
	// @blessed-invariant 15: Open fires inside the acquisition transaction.
	Open(ctx context.Context, spec ClaimSpec) (ClaimResult, error)

	// Commit publishes staging into live (regional rw on staged_*) or
	// applies the on_commit policy (pick-policy claims). For direct rw:
	// substrate no-op. policyOverride is meaningful only for pick-policy
	// claims (driven by the §14.4.1 routing table); ignored otherwise.
	//
	// region and address come from the lock-holder row's region_data and
	// address columns (populated by Open).
	Commit(ctx context.Context, region []byte, address []byte, policyOverride string) error

	// Abandon discards staging or applies the on_give_up policy. For
	// direct rw: degenerate (cannot undo direct writes); supervisor logs
	// and proceeds. policyOverride is meaningful only for pick-policy
	// claims; ignored otherwise. Not called for read-only claims.
	Abandon(ctx context.Context, region []byte, address []byte, policyOverride string) error

	// Delete removes the live region. A third terminal action alongside
	// Commit and Abandon for nodes whose intent is deletion. Regional
	// claims only — pick-policy claims express deletion via Commit +
	// policyOverride = "delete".
	Delete(ctx context.Context, region []byte) error

	// Release tears down substrate-side read state for a read claim.
	// Fires only when the substrate registered such state at Open
	// (relevant for staged_async substrates; not exercised by any v1
	// store implementation).
	Release(ctx context.Context, region []byte, address []byte) error
}
