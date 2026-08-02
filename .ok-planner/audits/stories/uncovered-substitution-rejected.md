---
audit: uncovered-substitution-rejected
artifact: story:uncovered-substitution-rejected
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:32:02Z
---

# Registration rejects an uncovered substitution ref, naming the ref and the covering subscription entry

Supported. `validateSubstitutionRefCoverage` in the template validator checks every extracted `nodes.` and `messages.` substitution ref (drawn from the same four-surface scan used by the symmetry check: attribute schema, claim-producer selector, lock name, fan-out partition request) against the receiver's declared `subscribes:` entries, and on a miss emits a structured `substitution_ref_uncovered` error carrying the literal ref text and a `suggested_subscribes_entry` naming the sender node/message type and the implied signal type. An HTTP-registration scenario test (`TestRegistrationRejectsUncoveredSubstitution`) checks both a per-field attribute ref and a whole-pull attribute ref, asserting HTTP 400, the ref literal, and the exact suggested `{node, type, force_upstream_refresh}` triple; a further subtest confirms the retired event-substitution form is also rejected at registration rather than deferred to runtime.
