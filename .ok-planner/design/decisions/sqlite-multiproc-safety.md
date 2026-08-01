---
decision: sqlite-multiproc-safety
status: as-is
---

# The SQLite driver is safe for processes sharing one local file

## Choice

Two halves. (1) The SQLite-backed persistence driver's read-then-write operations are transactional — no bare read-then-write surfaces relying on in-process connection serialization — so immediate-mode transactions provide cross-process atomicity. (2) The SQLite-backed advisory locker's scheduler-tick and migration locks are filesystem-lock-based (lock files alongside the database file), so tick and migration exclusion hold across processes sharing the file — without this, two scheduler processes would sweep concurrently, the exact condition `decision:sweep-lock-skip-on-error` exists to prevent. The per-name and per-scope in-tx locks hold cross-process via immediate-mode transactions (see `concept:persistence-database`, `concept:advisory-lock`).

## Rationale

The safety must be real for any deliberate multi-process SQLite operator. Separate-files and network-filesystem topologies remain physically unsupportable and undetectable in-process.

## Alternatives

- A startup gate refusing multi-process SQLite — rejected: outside the all-in-one deployment rimsky defaults to Postgres, so an operator who overrides to SQLite has chosen deliberately, and gating a deliberate config choice is not this platform's policy.
