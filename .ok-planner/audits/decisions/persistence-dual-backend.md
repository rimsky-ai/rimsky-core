---
audit: persistence-dual-backend
artifact: decision:persistence-dual-backend
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:42Z
---

# Whether both Postgres and SQLite ship, selected by a driver field in the unified config

Supported. The single open call switches on a driver string carried in the unified configuration's persistence block and dispatches to one of exactly two registered adapters; the validator rejects an empty driver, an unknown driver, a missing per-driver block, and both blocks present at once, and a table test covers all eight of those cases. Both adapters are real and complete rather than one being a stub: each carries its own migration directory and its own implementation of every accessor, and the cross-driver parity suite runs the same several-hundred-case library against both, which is what makes the second backend's completeness checkable rather than asserted. The rationale's split — SQLite for dev and test, Postgres for production — matches the deployment surface: the all-in-one image bakes SQLite defaults and the ephemeral run verb writes a SQLite configuration, while a SQLite driver outside the single-process topology raises a startup warning naming the shared-local-file precondition.
