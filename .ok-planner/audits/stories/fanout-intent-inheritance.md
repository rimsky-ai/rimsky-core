---
audit: fanout-intent-inheritance
artifact: story:fanout-intent-inheritance
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:15Z
---

# Fan-out sub-claims inherit the parent claim's declared intent uniformly

Supported. `AcquireSubClaims` (`lib/runtime/runner_subclaim.go`) sets every sub-claim's persisted `Intent` field directly from the caller-supplied `ParentIntent` — a single assignment with no producer-specific branch, so inheritance does not depend on which claim producer is in play. `TestAcquireSubClaims_InheritsParentReadOnlyIntent` and `TestAcquireSubClaims_InheritsParentReadWriteIntent` (`lib/runtime/runner_subclaim_test.go`) drive this function against a real Postgres-backed test database with a two-way split, then read back both resulting sub-claim rows and assert each carries the parent's intent (`r` in one test, `rw` in the other) rather than a hardcoded default.
