---
audit: subscription-mounting-state
artifact: decision:subscription-mounting-state
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:05Z
---

# Publisher subscriptions are desired-state rows with all four states and a visible one

Supported. The persistence layer declares exactly the four states the decision names — mounting, active, failed, stopped — and nothing else. Instance-create inserts one row per declared publisher in the mounting state and returns without issuing a Subscribe call, asserted by a test that checks both the state and the absence of an inline RPC; the two exceptions are non-retryable and are stamped failed at insert with a reason, namely an unregistered publisher name and a config-resolution failure. The instance-detail response carries a per-subscription list whose items expose the state and the failure reason alongside the publisher name, kind, and message type, populated from the row table on every detail read. State transitions out of mounting go through compare-and-set on the expected prior state, so a row cannot be flipped from under a concurrent writer.
