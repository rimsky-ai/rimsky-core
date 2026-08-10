---
audit: audit-log-read
artifact: story:audit-log-read
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# Every auth-relevant action lands in a readable, filterable log

Supported. Each action the story names was provoked on a fresh stack and read
back through the audit route: all five record kinds the surface serves appeared —
key creates, revokes, rotates, access attempts and access denials — with the
minted, revoked and rotated keys named, the dry-run write recorded as dry-run and
not executed against the executed write's own record, and the three denials
distinguished by reason. All nine filters the route accepts narrowed correctly —
kind, key name, action, action prefix, target path, status, mode, time window,
and page size with a cursor that advanced — and two malformed filter values were
rejected with 400. Reading the log is itself gated: a read-only key was admitted
and a key without the audit grant was refused.
