---
concept: scope
status: as-is
aliases: []
references:
  - _discover/2026-05-10-byte-equal-scope-conflict.md
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
---

# Scope

## What it is

Scope is the opaque byte stream a `ClaimProducer.Open` returns to identify "what was acquired." Persisted as `col:rimsky_claim_handles.scope_data`. Compared byte-equally via `code:foundation/locks/conflict.go::ScopesByteEqual`. The producer parses its own selector DSL and emits canonical bytes; rimsky has no producer-specific code in the conflict predicate.

### Selector vs scope

The two terms name two ends of the resolution pipeline; conflating them is a common authoring error:

- The **selector** is the opaque text the graph author supplies in a node's `claims:` block (post-`{{...}}` substitution). The producer parses it. May contain unresolved substitution directives at template-author time (`{{nodes.<node>.attribute.<field>}}`, `{{params.<key>}}`, `{{claim.<alias>.payload.<field>}}`), resolved at dispatch.
- The **scope** is the resolved selector or pick-policy-picked identifier — the canonical-byte form the producer commits to representing this claim by. Returned in `OpenResponse.Acquired.scope`. Persisted with the claim handle as `scope_data`. Scopes never contain substitution directives — they are post-resolution.

`{{claim.<alias>.scope}}` substitution returns the resolved scope bytes verbatim into the consuming attribute path.

## Purpose

Rimsky has to detect "these two claims target the same data" across producers it knows nothing about. Pushing canonicalization to the producer and using byte-equal comparison in rimsky keeps the conflict predicate uniform regardless of producer semantics.

The rationale for byte-equal-conflict (rather than richer producer-specific semantics):

1. **Single conflict predicate across heterogeneous producers.** Rimsky cannot reason about producer-specific selector DSLs (one might be POSIX glob, another might be SQL row-range, another might be regex over a custom namespace). Pushing canonicalization to the producer reduces the rimsky-side check to one `bytes.Equal` call, no per-producer code.
2. **Producer authorship is the canonicalization contract.** A producer that wants to honor "different selectors that target the same data" must canonicalize them to byte-equal scopes before returning from `Open`. The reference filesystem producer enforces this by requiring absolute concrete paths only.
3. **Audit-trail honesty.** The persisted `scope_data` bytes are exactly what the producer returned. No lossy normalization happens at the rimsky persistence boundary.

## Boundaries

Owns: the conflict-check comparison, the schema column, inertness discipline at all rimsky-side sites. Does NOT own: canonicalization (producer's job), capacity counting (named-lock's job), claim payload/address (other inert streams). Adjacent: `claim`, `claim-handle`, `claim-producer`, `write-semantics`, `inertness`.

## Invariants

- Scope comparison is `bytes.Equal`. Empty slices never conflict.
- Producers maintain the byte-equal-scope **uniformity invariant**: two `Open` calls with byte-equal scope MUST return the same `realized_write_semantics` (spec §2.5). Rimsky relies on this; does not verify it.
- The standard filesystem producer is concrete-paths only (canonicalizes by requiring absolute paths so byte-equality holds).
- Scope content is inert in rimsky (`@blessed-invariant 20`).

## Aliases and historical names

None live. The pre-v3 codebase used "region" as a synonym; that term is fully retired (per `spec:2026-05-12-nomenclature-resolution` Group A baseline rebase + Group B.8 in-code comment cleanup). Resolves `tension:_resolved/region-vs-scope-legacy`.

## Open within this concept

(none live; the `region` legacy-synonym tension was resolved by the schema rebase and the in-code comment removal in `code:foundation/locks/conflict.go`.)

## Common pitfalls

- **Rimsky's scope is not JavaScript variable scope, AWS resource scope, or OAuth scope.** A Rimsky scope is a producer-defined slice of its own state namespace; nothing to do with lexical scoping in programming languages, AWS IAM/resource grouping, or OAuth permission grants.
- Confusing selector with scope. The selector is what the template author writes (and may contain unresolved substitution directives); the scope is the canonical-byte form returned by the producer post-acquisition.
- Implementing a producer that doesn't canonicalize scope bytes. Two claims that should conflict but produce different scope bytes will NOT be detected as conflicting; the producer is responsible for normalizing.

## Notes

- [2026-05-18] Folded content from former `docs/concepts/scope.md` (now retired) — selector-vs-scope authoring distinction added as a subsection under "What it is"; JS-scope / AWS-scope / OAuth-scope disambiguation + producer-canonicalization-discipline pitfalls added as a Common-pitfalls section.
