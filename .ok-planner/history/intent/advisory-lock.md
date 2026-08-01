# Intent Dossier: advisory-lock

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Advisory locks are the named cross-process coordination mechanism for exactly three jobs: **migration ownership**, **scheduler-tick ownership**, and **per-scope serialization** (2026-06-08, corpus-bootstrap).
- Postgres uses pg advisory locks. SQLite's equivalent evolved: originally an in-process mutex with single-process the only supported topology (2026-05-04), superseded 2026-06-11 — the scheduler-tick and migration locks became **file-lock (flock) based** so exclusion holds across multiple rimsky processes sharing one local database file. Separate DB files per process and network filesystems remain unsupported.
- The dispatch/scheduler tick acquires a per-tick advisory lock; replicas failing to acquire skip the tick. An **error** from the lock attempt is treated as lock-held: log and skip, never run unlocked (2026-06-11).
- The persistence helper's type name is `AdvisoryLocker` (renamed from `Coordinator`, 2026-05-04).
- Multi-replica coordination for publishers/sensors is deliberately NOT rimsky's concern — no advisory-lock-per-subscription primitives (2026-05-17).

## Required behaviors (open promises)

All entries on this concept are artifact-tier.

- Per-tick advisory lock on the dispatch/scheduler tick; replicas failing to acquire skip the tick. Migration runner holds a session-scoped advisory lock for the duration of a migration batch (2026-05-04, foundation-contract, artifact; corroborated 2026-06-08, corpus-bootstrap): "postgres advisory locks + sqlite equivalent at session level. Rationale: migration ownership, scheduler-tick ownership, per-scope serialization."
- Lock-attempt error handled as lock-held in the scheduler tick — "an error from the advisory-lock attempt is treated as lock-held: log and skip, never run unlocked." Sweeps are periodic recovery; a one-interval delay is benign, running unlocked under DB flakiness is not (2026-06-11, last-mile-stability, artifact).
- SQLite multi-process safety: bare read-then-write call sites are transactional (immediate-mode transactions), and the scheduler-tick + migration locks are flock-based so exclusion holds across processes. Deliberately **no startup gate** — "gating a deliberate config choice is not this platform's policy" (2026-06-11, last-mile-stability, artifact).
- `SweepClaimHandleRetention` (terminal claim-handle rows past `retention.claim_handles_trailing`, default 30d) runs on the scheduler tick, serialized across replicas by the scheduler-tick advisory lock (2026-05-17, post-data-platform-cleanup, artifact) (artifact-only).
- The two-guard-shape model for claimant-guarded release (blessed invariant 4): active-row mutations carry `AND holder_supervisor_id = supervisor_id`; non-active-row deletions (retention sweep, Release-path Delete) carry no per-row claimant guard and are instead guarded by the NULL-holder CHECK constraint, the scheduler-tick advisory lock serializing the sweep, and the Release path's row-discovery filter (2026-05-17, post-data-platform-cleanup, artifact) (artifact-only).

## Intentional absences

- **No standalone scheduler concept** — deliberately not promoted: the scheduler binary is a thin advisory-lock-gated cron loop already covered by the schedule and advisory-lock concepts plus module-layout; the supervisor carries the load-bearing surface. Likewise no separate migrate-binary concept (2026-05-11, log-convergence, rejection).
- **No advisory-lock-per-subscription / publisher HA primitives in rimsky** — no heartbeat protocol, no periodic resync-to-detect-drift. HA is the publisher implementation's responsibility; all four bundled sensors declare single-replica deployment models. "The 'advisory lock per subscription' framing from the earlier plan was solving a problem that doesn't exist in this architecture" (2026-05-17, sensor-messaging-unification, rejection — explicitly supersedes post-data-platform-cleanup item 5).
- **No sensor-cron per-watch advisory lock** — the 2026-05-15 scoped feature was never implemented and its re-promise was overtaken by the 2026-05-17 single-replica rejection above. Its absence from code is by design today; what must NOT survive is any doc/comment claiming it exists (see drift record below).

## Corrections and restorations (drift-fight record)

- **sensor-cron phantom feature** (2026-05-17, post-data-platform-cleanup-notes): the file's doc comment claimed "Multi-replica deployments are guarded by a per-watch `pg_try_advisory_lock`" while the code contained no advisory-lock call, no SENSOR_CRON_KEY, no database integration — only an in-process `sync.Mutex`. The feature was scoped in 2026-05-15 data-platform-extensions and silently dropped; only a single-replica behavior-pinning test existed. Precedent: doc claimed, code didn't → the doc was the drift. The same session's design re-promised the multi-replica behavior, but the 2026-05-17 sensor-messaging-unification rejection then removed multi-replica sensor coordination from rimsky's scope entirely — so the resolution is *fix the doc / drop the claim*, not *implement the lock*.

## Superseded / historical

- SQLite in-process mutex with "single-process is the only supported topology" (2026-05-04, foundation-contract) → superseded by flock-based cross-process locks + transactional call sites (2026-06-11, last-mile-stability, which also resolved the sqlite-vs-memory-reject-asymmetry tension).
- `persistence.Coordinator` naming → renamed `AdvisoryLocker` (2026-05-04, layer-crystallization).
- sensor-cron multi-replica advisory-lock serialization (scoped 2026-05-15, data-platform-extensions; re-promised 2026-05-17 post-data-platform-cleanup) → closed by the 2026-05-17 sensor-messaging-unification rejection (single-replica sensors; HA is the publisher's problem).
