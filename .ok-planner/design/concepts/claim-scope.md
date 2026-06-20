---
concept: claim-scope
status: as-is
aliases: []
---

# Claim Scope

## What it is

ClaimScope is the opaque byte stream a claim producer's open verb returns to identify "what was acquired." Persisted on the claim-handle ledger. Compared byte-equally by the rimsky-side conflict predicate. The producer parses its own selector DSL and emits canonical bytes; rimsky has no producer-specific code in the conflict predicate.

### Selector vs claim scope

The two terms name two ends of the resolution pipeline; conflating them is a common authoring error:

- The **selector** is the opaque text the graph author supplies in a node's claim declaration (post-substitution). The producer parses it. May still carry unresolved substitution directives at template-author time, resolved at dispatch.
- The **claim scope** is the resolved selector or pick-policy-picked identifier — the canonical-byte form the producer commits to representing this claim by. Returned by the producer's open verb and persisted with the claim handle. Claim scopes never contain substitution directives — they are post-resolution.

A claim-scope substitution path returns the resolved claim scope bytes verbatim into the consuming attribute path.

## Purpose

Rimsky has to detect "these two claims target the same data" across producers it knows nothing about. Pushing canonicalization to the producer and using byte-equal comparison in rimsky keeps the conflict predicate uniform regardless of producer semantics.

The rationale for byte-equal-conflict (rather than richer producer-specific semantics):

1. **Single conflict predicate across heterogeneous producers.** Rimsky cannot reason about producer-specific selector DSLs (one might be POSIX glob, another might be SQL row-range, another might be regex over a custom namespace). Pushing canonicalization to the producer reduces the rimsky-side check to one byte-equality comparison, no per-producer code.
2. **Producer authorship is the canonicalization contract.** A producer that wants to honor "different selectors that target the same data" must canonicalize them to byte-equal claim scopes before returning from the open verb.
3. **Audit-trail honesty.** The persisted claim scope bytes are exactly what the producer returned. No lossy normalization happens at the rimsky persistence boundary.

## Boundaries

Owns: the conflict-check comparison, the schema column, inertness discipline at all rimsky-side sites. Does NOT own: canonicalization (producer's job), capacity counting (named-lock's job), claim payload/address (other inert streams). Adjacent: `claim`, `claim-handle`, `claim-producer`, `write-semantics`, `inertness`.

## Invariants

- Claim scope comparison is byte-equality. Empty byte streams never conflict.
- Producers maintain the byte-equal-claim-scope **uniformity invariant**: two open calls with byte-equal claim scope MUST return the same realized write semantics. Rimsky relies on this; does not verify it.
- Claim scope content is inert in rimsky (invariant 20).

## Common pitfalls

- Confusing selector with claim scope. The selector is what the template author writes (and may contain unresolved substitution directives); the claim scope is the canonical-byte form returned by the producer post-acquisition.
- Implementing a producer that doesn't canonicalize claim scope bytes. Two claims that should conflict but produce different claim scope bytes will NOT be detected as conflicting; the producer is responsible for normalizing.
- Confusing **ClaimScope** (this concept; claim-identity bytes) with **RunScope** (`concept:run-scope`; execution-context). The two share the "Scope" suffix but name entirely different things — ClaimScope is for claim conflict detection; RunScope is for "which graph instantiation does this run belong to." Both carry qualifying prefixes; bare `Scope` is never used.
