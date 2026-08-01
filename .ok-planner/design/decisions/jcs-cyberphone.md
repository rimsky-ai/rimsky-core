---
decision: jcs-cyberphone
status: as-is
---

# JSON canonicalization for spec hashing

## Choice

A spec-compliant JCS canonicalization library, version-pinned permanently: the pin moves only as a deliberate act accompanied by a decision on template-identity migration, never as a routine dependency bump.

## Rationale

The only Go implementation compliant with the canonicalization spec — and its exact output bytes are load-bearing: every template id is a hash over them, so a silently moved pin whose output bytes change splits template identity between persisted ids and newly computed ones (see `concept:template`).

## Alternatives

- Track the library's releases like an ordinary dependency — rejected: a routine bump that changes output bytes silently orphans every persisted template id, and the id format carries no scheme marker that could tell the two generations apart.
- Version-prefix the template id so a canonicalization-scheme change ships as a structurally new identity — deliberately not taken up: a breaking format change with permanent parsing cost, unjustified while nothing requires the pin to move. Reconsidered only if a concrete need to move the pin appears.
