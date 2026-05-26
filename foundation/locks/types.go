// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Local value types for the named-lock primitive.
//
// Two primitives split (spec §2.1 / glossary):
//
//   - Claim — producer-bound; the ClaimSpec value type carries
//     (ProducerName, Selector, Intent, Alias). The producer parses
//     Selector and decides what it means (scoped access vs. configured
//     pick policy). The claim-producer contract value types (ClaimSpec,
//     ClaimResult, OpenOutcome, WriteSemantics, Intent, Capabilities, …)
//     live in their canonical Go home, github.com/fallguyconsulting/rimsky/
//     protocols/claimproducer; rimsky-internal code imports them directly
//     from there.
//
//   - Named lock — producer-independent; NamedLockSpec carries (Name)
//     only. Limit lives in operator config.
//
// Two types, no common interface: the claim-producer ClaimSpec and
// NamedLockSpec are distinct. Callers dispatch by type. NamedLockSpec is
// rimsky-internal — it has no protocol-layer equivalent because named
// locks never cross the wire to a producer, so it is declared here rather
// than in protocols/claimproducer.

package locks

// NamedLockSpec is the producer-independent named-lock primitive.
// Templates reference named locks by name only; the limit (mutex vs.
// counting) lives in the operator's named_locks: config block.
//
// NamedLockSpec is rimsky-internal — it has no protocol-layer
// equivalent because named locks never cross the wire to a producer.
type NamedLockSpec struct {
	Name string
}
