---
concept: scope
definition: |
  The slice of a producer's namespace under claim. Both the conceptual `(producer, selector)` pair and the concrete opaque bytes that identify it on the claim-handle row. Conflict checking compares scope bytes byte-for-byte; producers canonicalize scope bytes such that two claims that should conflict produce byte-equal scopes.
proto_symbol: OpenRequest in protocols/proto/v1/claim_producer.proto
config_field: (none)
api_surface: (none)
related: [claim, claim-producer, claim-handle, write-semantics]
deprecated_terms: [region]
---

# Scope

## Definition

The slice of a producer's namespace under claim. Both the conceptual `(producer, selector)` pair and the concrete opaque bytes that identify it on the claim-handle row. Conflict checking compares scope bytes byte-for-byte; producers canonicalize scope bytes such that two claims that should conflict produce byte-equal scopes.

## Why it exists

Rimsky needs to detect when two claims overlap, but it cannot understand the structure of any specific producer's state. The solution: the producer canonicalizes its own scopes. Two claims that should conflict produce byte-equal scope bytes; rimsky then runs byte-equality on the persisted scope bytes to detect the conflict.

This puts the scope-equivalence rules in the right place. The producer knows whether `/foo/bar/` and `/foo/bar` should conflict, whether `analytics_production` and `analytics_PRODUCTION` should be normalized to the same scope, whether trailing-slash matters. Rimsky is opinion-free; it just compares bytes.

## Selector vs. scope

The **selector** is the opaque text the graph author supplies (post-substitution). The producer parses it.

The **scope** is the resolved selector or pick-policy-picked identifier — the canonical-byte form the producer commits to representing this claim by. Returned in `OpenResponse.scope` (named `Acquired.scope` in the proto). Persisted with the claim handle as the scope bytes.

Selectors may contain `{{...}}` substitution directives resolved at dispatch (`{{nodes.<node>.attribute.<field>}}`, `{{params.<key>}}`, `{{claim.<alias>.payload.<field>}}`); scopes never do — they are post-resolution.

## How you encounter it

- **Wire**: `OpenRequest` carries `selector`; `OpenResponse.acquired.scope` carries the resolved scope bytes.
- **Templates**: the `selector:` field of each claim declaration in a node's `stores:` block. May contain `{{...}}` substitution directives.
- **Substitution**: `{{claim.<alias>.scope}}` returns the resolved scope bytes verbatim into the consuming attribute path.

## Consumer-visible guarantees

- Scope content is opaque to Rimsky. Rimsky reads claim content (including scope) by named-field path only at substitution-leaf extraction; it does not log, validate, transform, or otherwise act on the bytes.
- Across the lifetime of a producer, two `Open` calls returning byte-equal scope must return the same realized write semantics — the byte-equal-scope uniformity property. Producers enforce this; the foundation relies on it for the conflict predicate.

## Common mistakes

- **Rimsky's scope ≠ JavaScript variable scope ≠ AWS resource scope.** A Rimsky scope is a producer-defined slice of its own state namespace; nothing to do with lexical scoping in programming languages or AWS resource grouping.
- Confusing selector with scope. The selector is what the template author writes (and may contain unresolved substitution directives); the scope is the canonical-byte form returned by the producer post-acquisition.
- Implementing a producer that doesn't canonicalize scope bytes. Two claims that should conflict but produce different scope bytes will NOT be detected as conflicting; the producer is responsible for normalizing.

## See also

- [`claim.md`](claim.md)
- [`claim-handle.md`](claim-handle.md)
- [`claim-producer.md`](claim-producer.md)
- [`write-semantics.md`](write-semantics.md)
