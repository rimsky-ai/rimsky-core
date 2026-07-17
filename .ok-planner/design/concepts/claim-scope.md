---
concept: claim-scope
status: as-is
aliases: []
---

# Claim Scope

## What it is

ClaimScope is the opaque byte stream that identifies "what was acquired": returned by a claim producer's open verb, or supplied per sub-claim by the producer's split-scope verb during fan-out partitioning. Persisted on the claim-handle ledger. The default conflict predicate is byte-equality; a producer that advertises the scopes-conflict capability supplies its own overlap predicate instead, and rimsky delegates the conflict decision to it (see Invariants). The producer parses its own selector DSL and emits canonical bytes.

### Selector vs claim scope

The two terms name two ends of the resolution pipeline; conflating them is a common authoring error:

- The **selector** is the opaque text the graph author supplies in a node's claim declaration. At template-author time it may still carry unresolved substitution directives; these resolve at dispatch, and the producer parses the dispatch-time-resolved text.
- The **claim scope** is the resolved selector or, for a producer that picks among candidates rather than matching a literal selector (a pick policy), the identifier it picked — the canonical-byte form the producer commits to representing this claim by. Returned by the producer's open verb and persisted with the claim handle. Claim scopes never contain substitution directives — they are post-resolution.

A claim-scope substitution path returns the resolved claim scope bytes verbatim into the consuming attribute path.

## Purpose

Rimsky has to detect "these two claims target the same data" across producers it knows nothing about. The default conflict predicate is byte-equal comparison of claim scope bytes; a producer may instead advertise the scopes-conflict capability and supply its own overlap predicate, consulted at acquisition (including the fan-out sub-claim path) in place of byte-equality.

The rationale for byte-equal-conflict as the default (rather than mandating richer producer-specific semantics):

1. **Uniform default across heterogeneous producers.** Rimsky cannot reason about producer-specific selector DSLs (one might be POSIX glob, another might be SQL row-range, another might be regex over a custom namespace). Byte-equality is the predicate rimsky can evaluate without any producer-specific code; a producer with richer overlap semantics opts into its own predicate via the scopes-conflict capability instead of relying on canonicalization tricks.
2. **Producer authorship is the canonicalization contract.** A producer that wants byte-equal claim scopes to line up for the data it considers identical must canonicalize at the open verb; a producer that instead wants to honor "different selectors that target the same data" without forcing a byte-equal canonical form advertises the scopes-conflict capability and answers the overlap question directly.
3. **Audit-trail honesty.** The persisted claim scope bytes are exactly what the producer returned. No lossy normalization happens at the rimsky persistence boundary.

## Boundaries

Owns: the conflict-check comparison, the schema column, inertness discipline at all rimsky-side sites. Does NOT own: canonicalization (producer's job), capacity counting (named-lock's job), claim payload/address (other inert streams). Adjacent: `claim`, `claim-handle`, `claim-producer`, `write-semantics`, `inertness`.

## Invariants

- Claim scope comparison is byte-equality by default; empty byte streams never conflict under the default predicate. A producer that advertises the scopes-conflict capability supplies its own overlap predicate instead, consulted at acquisition and in the fan-out sub-claim path — rimsky imposes no byte-equality or empty-never-conflicts guarantee on that path; the producer owns the emptiness/overlap semantics for its own predicate.
- Producers maintain the byte-equal-claim-scope **uniformity invariant**: two open calls with byte-equal claim scope MUST return the same realized write semantics. Rimsky relies on this; does not verify it.
- Claim scope content is inert in rimsky (invariant 20).

## Common pitfalls

- Confusing selector with claim scope. The selector is what the template author writes (and may contain unresolved substitution directives); the claim scope is the canonical-byte form returned by the producer post-acquisition.
- Implementing a producer that doesn't canonicalize claim scope bytes and doesn't advertise the scopes-conflict capability. Two claims that should conflict but produce different claim scope bytes will NOT be detected as conflicting under the byte-equal default; the producer must either normalize to byte-equal claim scopes or advertise scopes-conflict and answer the overlap question itself.
- Confusing **ClaimScope** (this concept; claim-identity bytes) with **RunScope** (`concept:run-scope`; execution-context). The two share the "Scope" suffix but name entirely different things — ClaimScope is for claim conflict detection; RunScope is for "which graph instantiation does this run belong to." Both carry qualifying prefixes; bare `Scope` is never used.
