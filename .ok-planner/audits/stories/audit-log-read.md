---
audit: audit-log-read
artifact: story:audit-log-read
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:39:20Z
---

# An operator reads and filters the auth-relevant action audit

Supported. Every action the story names was provoked against a fresh all-in-one
deployment and then read back through the audit surface. All five record kinds
the story enumerates were present — four key creations, one revocation, one
rotation, nine access attempts and three denials — and each carried what an
operator needs to attribute it: the minted keys by name, the revoked and rotated
keys by name, the dry-run write recorded in dry-run mode as not executed against
the real write's execute mode and executed, and the three denials distinguished
by reason (invalid token, no token, insufficient permission). All nine filters
the surface accepts were exercised and each narrowed as claimed — record kind,
key name, exact action, action prefix, target path, response status, mode,
timestamp lower bound, and page size with a cursor that paged to a different
record — and two malformed filter values (a record kind outside the auth set, a
non-RFC3339 timestamp) were rejected with 400. Reading the log is itself gated: a
read-only key was admitted and a key without the audit-read action was refused.
