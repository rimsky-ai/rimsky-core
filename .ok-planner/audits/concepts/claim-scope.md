---
audit: claim-scope
artifact: concept:claim-scope
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# ClaimScope as the opaque claim-identity byte stream, its conflict predicate, and its naming vocabulary

Supported. All four invariants hold in the code as it stands. The default conflict predicate is byte-equality with an empty stream never conflicting, implemented once in the foundation locks package over the protocol package's fallback helper and unit-tested there; a producer advertising the scopes-conflict capability replaces it, and the single helper that makes that choice is called from both conflict sites — the ordinary acquisition path and the fan-out sub-claim path — with an end-to-end scenario driving a purpose-built overlap producer through a real stack. The uniformity invariant is stated as a producer obligation rimsky does not verify, and nothing in the runtime verifies it, which is what the invariant says. Claim scope bytes stay uninspected on the rimsky side: the runtime marshals the resolved selector to seed the ledger row before the open verb, replaces it only when the producer returns non-empty different bytes, and never parses either. The canonical naming vocabulary is enforced by a repo-wide fitness test that scans every Go, SQL, proto and YAML file in the tree outside the design corpus; the persisted column is `claim_scope_data` in both the Postgres and SQLite migration sets, the advisory-lock kind is `claim_scope` in both, the byte-equality helper carries the `ClaimScopes` prefix, and an independent scan for the four retired terms found no occurrence outside that test's own forbidden-term list. The claim-scope substitution path shares one stringification helper with the address path, unquoting a JSON-string scope and passing any other JSON form through as raw text, with unit tests for both. One accuracy note on the Purpose rationale rather than on any invariant: under the Postgres backend the scope column is JSONB, so a producer-returned object is re-rendered by the store rather than preserved byte-for-byte, and the persistence conformance suite compares stored scopes to their inputs semantically rather than by bytes.
