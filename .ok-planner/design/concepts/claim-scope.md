---
concept: claim-scope
status: as-is
aliases: [scope (pre-2026-05-22, retired)]
---

# Claim Scope

## What it is

ClaimScope is the opaque byte stream a claim producer's open verb returns to identify "what was acquired." Persisted as the claim-scope-data field on the claim-handle ledger. Compared byte-equally by the rimsky-side conflict predicate. The producer parses its own selector DSL and emits canonical bytes; rimsky has no producer-specific code in the conflict predicate.

### Selector vs claim scope

The two terms name two ends of the resolution pipeline; conflating them is a common authoring error:

- The **selector** is the opaque text the graph author supplies in a node's `claims:` block (post-`{{...}}` substitution). The producer parses it. May contain unresolved substitution directives at template-author time (`{{nodes.<node>.attribute.<field>}}`, `{{params.<key>}}`, `{{claim.<alias>.payload.<field>}}`), resolved at dispatch.
- The **claim scope** is the resolved selector or pick-policy-picked identifier — the canonical-byte form the producer commits to representing this claim by. Returned in the acquired-result claim-scope field. Persisted with the claim handle as the claim-scope-data field. Claim scopes never contain substitution directives — they are post-resolution.

The `{{claim.<alias>.claim_scope}}` substitution returns the resolved claim scope bytes verbatim into the consuming attribute path.

## Purpose

Rimsky has to detect "these two claims target the same data" across producers it knows nothing about. Pushing canonicalization to the producer and using byte-equal comparison in rimsky keeps the conflict predicate uniform regardless of producer semantics.

The rationale for byte-equal-conflict (rather than richer producer-specific semantics):

1. **Single conflict predicate across heterogeneous producers.** Rimsky cannot reason about producer-specific selector DSLs (one might be POSIX glob, another might be SQL row-range, another might be regex over a custom namespace). Pushing canonicalization to the producer reduces the rimsky-side check to one byte-equality comparison, no per-producer code.
2. **Producer authorship is the canonicalization contract.** A producer that wants to honor "different selectors that target the same data" must canonicalize them to byte-equal claim scopes before returning from the open verb. The reference filesystem producer enforces this by requiring absolute concrete paths only.
3. **Audit-trail honesty.** The persisted claim scope bytes are exactly what the producer returned. No lossy normalization happens at the rimsky persistence boundary.

## Boundaries

Owns: the conflict-check comparison, the schema column, inertness discipline at all rimsky-side sites. Does NOT own: canonicalization (producer's job), capacity counting (named-lock's job), claim payload/address (other inert streams). Adjacent: `claim`, `claim-handle`, `claim-producer`, `write-semantics`, `inertness`.

## Invariants

- Claim scope comparison is byte-equality. Empty byte streams never conflict.
- Producers maintain the byte-equal-claim-scope **uniformity invariant**: two open calls with byte-equal claim scope MUST return the same realized write semantics (spec §2.5). Rimsky relies on this; does not verify it.
- The standard filesystem producer is concrete-paths only (canonicalizes by requiring absolute paths so byte-equality holds).
- Claim scope content is inert in rimsky (`@blessed-invariant 20`).

## Aliases and historical names

The pre-v3 codebase used "region" as a synonym; that term is fully retired (per `spec:2026-05-12-nomenclature-resolution` Group A baseline rebase + Group B.8 in-code comment cleanup). Resolves `tension:_resolved/region-vs-scope-legacy`.

Renamed from `scope` to `claim-scope` per `spec:2026-05-22-fan-out-safety-scope-first-design`, to disambiguate from `concept:run-scope` (the execution-context concept). The legacy bare-`scope` term is fully retired.

## Open within this concept

(none live; the `region` legacy-synonym tension was resolved by the schema rebase and the in-code comment removal in the conflict predicate.)

## Common pitfalls

- Confusing selector with claim scope. The selector is what the template author writes (and may contain unresolved substitution directives); the claim scope is the canonical-byte form returned by the producer post-acquisition.
- Implementing a producer that doesn't canonicalize claim scope bytes. Two claims that should conflict but produce different claim scope bytes will NOT be detected as conflicting; the producer is responsible for normalizing.
- Confusing **ClaimScope** (this concept; claim-identity bytes) with **RunScope** (`concept:run-scope`; execution-context). The two share the "Scope" suffix but name entirely different things — ClaimScope is for claim conflict detection; RunScope is for "which graph instantiation does this run belong to." Both carry qualifying prefixes; bare `Scope` is never used.

## Notes

- [2026-05-18] Folded content from a former, now-retired scope doc — selector-vs-scope authoring distinction added as a subsection under "What it is"; JS-scope / AWS-scope / OAuth-scope disambiguation + producer-canonicalization-discipline pitfalls added as a Common-pitfalls section.
- [2026-05-22] Renamed from the now-retired bare `scope` concept to `concept:claim-scope` per `spec:2026-05-22-fan-out-safety-scope-first-design` to make room for `concept:run-scope`.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
