---
audit: emit-work-completed
artifact: decision:emit-work-completed
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:36Z
---

# The ledger speaks both halves of work

Supported as a mechanism. Every non-retry terminal application (complete, errored, infra-errored) is wrapped in a post-commit step that appends a work-completed event carrying the same dispatch-id and supervisor-id identifiers as its work-started twin, plus the terminal kind. A dedicated test suite exercises the complete, errored, and park-suppressed cases and asserts the identifier fields match between a run's work-started and its work-completed event. This decision's scope is the emission mechanism itself — the event kind is no longer declared-but-unemitted; whether every work-started in the system gets exactly one paired work-completed is the broader claim of `story:work-completed-emitted`, audited separately and found to have a gap on the liveness-recovery path.
