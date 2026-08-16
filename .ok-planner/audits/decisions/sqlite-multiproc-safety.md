---
audit: sqlite-multiproc-safety
artifact: decision:sqlite-multiproc-safety
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:36:14Z
---

# Whether the embedded-file driver is genuinely safe for multiple processes sharing one database file

Supported, on both halves. For the second half — the filesystem locks — the embedded driver's advisory locker holds no database state at all: it derives two lock-file paths from the database path and takes a real kernel file lock on each, one for the scheduler tick and one for migrations, with a Unix and a Windows implementation of the same try-lock. Three tests cover it: tick exclusion across two locker instances plus re-acquisition after release, migration blocking across two instances plus hand-off, and context cancellation while another holds it. Kernel file locks are per-open-file, so two locker instances in one process is a faithful stand-in for two processes. For the first half — no bare read-then-write — the structural guard is stronger than a convention: the table-accessor executor panics on a nil transaction, so every one of those methods is unrepresentable outside a transaction, and the connection string sets the immediate transaction lock, making every transaction a write transaction from BEGIN. The queue's transaction-less methods are the one place the pool is touched directly; enumerating all twenty-two direct pool statements across the driver, each is a single statement in its own method, and every mutating one expresses its guard as a WHERE predicate rather than a prior read. The single exception is the frame-trace retention prune, which snapshots prunable ids on the pool and then does its deletes in a transaction — and that runs only inside the scheduler tick, behind the very tick lock the second half provides. The claim that the per-name and per-scope in-transaction locks (all three no-ops on this driver) hold via the immediate transaction lock is pinned by a sixteen-racer check-then-insert test that would show a lost update if the pragma stopped closing that window.
