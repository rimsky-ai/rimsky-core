---
audit: advisory-locks
artifact: decision:advisory-locks
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:30:00Z
---

# Advisory locks per coordination job, session-scoped versus transaction-scoped

Supported. The advisory-locker interface carries five primitives across the three job categories the decision names, and the client-server driver implements the lifetime split exactly as stated: migration and scheduler tick take native session-level locks on two distinct pinned keys held on a dedicated pooled connection, while the per-scope trio — per-name, per-claim-scope, and per-lifecycle-scope — take transaction-level locks under three distinct key classes and release with the transaction. The migration lock is acquired once around the whole batch and released on return, so it spans the batch rather than a single file, and the tick lock is a non-blocking try that hands back a release closure. The embedded driver carries the two session-scoped jobs through exclusive file locks on paths derived from the database file, proven cross-instance by two unit tests, and makes the three in-tx primitives no-ops on the ground that the immediate-mode transaction's writer-slot hold already closes the same window — a substitution stated positively in the sibling concept and the companion multi-process decision rather than in this decision's own text, and the only place the "equivalent" is a different mechanism instead of a lock. Both rejected alternatives are genuinely absent: there is no lock table with acquire and release rows anywhere in the schema's 25 ledgers, and no external coordination service in the dependency set.
