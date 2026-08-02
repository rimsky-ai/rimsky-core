---
audit: event-log-payload-shapes
artifact: decision:event-log-payload-shapes
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095821-event-log-payload-shape-polarity-reversed
---

# Event log payload shape

Unsupported — the polarity is the exact inverse of what the codebase enforces. The typed-oneof wire format carries a subset of operational event kinds, not signal-class kinds; every signal-class kind is excluded from the typed oneof by construction and always falls to the free-form payload field, and this exact mapping is mechanically enforced by a test whose own description names the oneof as reserved for operational kinds. Separately, the platform's actual internal write and read path is free-form for every event kind alike, signal-class and operational; no production code anywhere constructs the oneof-carrying wire message, so the typed oneof is presently unconsumed by any internal path regardless of which polarity is intended.
