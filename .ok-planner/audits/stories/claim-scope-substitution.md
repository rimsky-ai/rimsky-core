---
audit: claim-scope-substitution
artifact: story:claim-scope-substitution
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Canonical `claim.<alias>.claim_scope` resolves; the abbreviated `.scope` spelling is refused

The story is supported. At runtime, `lib/graph/attribute`'s directive resolver recognizes `claim.<alias>.claim_scope` and returns the live claim's claim-scope bytes, while the abbreviated `claim.<alias>.scope` falls through to the same function's default branch and returns a missing-source error naming the canonical `claim_scope` segment — exercised by a unit test asserting the canonical form resolves to the claim's scope value and the legacy form errors. Separately, the template validator's directive checker rejects `{{claim.<alias>.scope}}` at registration with a message naming `claim_scope` as the expected segment, and a registration-level test asserts the canonical spelling validates cleanly while the legacy spelling is rejected with a message containing `claim_scope`. Both the runtime and registration enforcement points were checked; no other claim-scope spelling variant exists in the directive grammar.
