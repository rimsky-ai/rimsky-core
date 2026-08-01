---
decision: spec-jcs-canonicalization
status: as-is
---

# Template-spec hashing canonicalizes with RFC 8785 JCS

## Choice

The template-spec hash is computed over canonical bytes produced by RFC 8785 JCS (the JSON Canonicalization Scheme).

## Rationale

A standardized, cross-implementation canonicalization makes the hash deterministic and reproducible from the spec's semantic content — key order and whitespace never perturb template identity — without rimsky maintaining a bespoke scheme.

## Alternatives

- Hash the submitted spec bytes verbatim — rejected: semantically identical specs differing only in key order or whitespace would hash to different identities.
- A hand-rolled sorted-key marshal — rejected: a bespoke canonicalization with no published specification to hold independent implementations together.
