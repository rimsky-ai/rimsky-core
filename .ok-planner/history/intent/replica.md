# Intent Dossier: replica

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Rimsky models **named peers (one per config entry), never pod counts**. Replicas are not individually addressable, do not share rimsky-runtime state, and rimsky does not detect, heartbeat, or fail over replicas — replica count is purely a deployment-tier knob (k8s replicas, compose deploy.replicas) (2026-05-17, sensor-messaging-unification).
- Replica-safety inside rimsky's core is advisory-lock-based: the dispatch tick takes a per-tick advisory lock (replicas failing to acquire skip the tick); the migration runner holds a session-scoped advisory lock for a batch. Postgres uses pg advisory locks. CORRECTED 2026-07-20 (queued discovery, phase-3 step-4 dossier reconciliation): SQLite does **not** use an in-process mutex — the cross-process `flock`-based advisory locker (`lib/foundation/persistence/sqlite/advisory_locker.go`, OS-level file locks on `<dbpath>.tick.lock` / `<dbpath>.migrate.lock`) landed 2026-06-13, before this line was written, and single-process is **not** the only supported SQLite topology: multi-process/multi-replica SQLite is supported outside the unified topology as long as every process shares one local (non-network) database file — the shipped warn-only gate's own text already says so (`persistence.SQLiteReplicaWarning`, `lib/foundation/persistence/topology.go`: "cross-process coordination (advisory locks, transactional writer-slot holds) is safe, but every role process and every replica must share one local (non-network) database file").
- Multi-replica coordination for publishers/sensors is deliberately NOT rimsky's concern: no advisory-lock-per-subscription, no heartbeat protocol, no drift-detecting resync. Bundled sensors declare single-replica deployment models backed by state persistence; **N independent replicas honestly fire N times per window** — that is the pinned v1 contract, not a bug.
- Two supported rimsky deployment topologies, each with a standing integration proof: the unified single-process all-in-one (one OS process serving all three roles) and the three-container split against shared Postgres.

## Required behaviors (open promises)

- Per-tick advisory lock on dispatch; session advisory lock on migration batches (2026-05-04, foundation-contract, artifact); the original SQLite single-process posture was superseded 2026-06-13 by the cross-process flock advisory locker — see Net position.
- Anonymous-mode predicate cached per control-api replica with a short TTL (default 1s) as the only freshness mechanism; each replica refreshes independently; no cross-process invalidation channel (2026-05-15, control-plane-mcp-and-auth, artifact-only).
- Sensor-cron replica posture documented-and-true: one replica fires each window exactly once; N replicas sharing a publisher_subscription_id fire N times (fireCount==2 pinned for two replicas); an accuracy check asserts no advisory-lock/leader-election primitive exists in sensor-cron source (2026-06-06, comprehensive-gap-closure, artifact).
- Watermark persistence, not coordination, is the sensor HA story: sensor-cron persists next-fire watermarks to a durable state DB (empty DSN = in-memory), restart preserves the subscription and fires the originally-scheduled window, advancement from the row's prior next_fire_at (2026-06-08, corpus-bootstrap, artifact).
- The no-command entrypoint runs migrations synchronously then all three roles **in one OS process** with a single signal-handled shutdown; `RIMSKY_PROCESS_ROLE=unified` is set only there; single-role deployments unchanged (2026-06-11, last-mile-stability, artifact).
- Both topologies carry standing services-harness proofs: all-in-one (one process serves all three role surfaces, node driven to terminal, memory-backend blob round-tripped across roles) and three-container split (same scenario over shared Postgres) (2026-06-11, artifact).
- `lib/control/launch` is the single shared wiring shape: importable role runners wrapping the role mains' full wiring (config.Start*, metrics refreshers, supervisor observability handshake, background loops) returning stop handles; role mains are thin shells, so unified and per-role invocations behave identically (2026-06-11, completion report, artifact).
- Launch-topology hardening direction (ratified sketch, 2026-06-21, ecde6dd1, transcript): typed Topology parameter replacing the stringly RIMSKY_PROCESS_ROLE seam; migrate ownership as a typed launcher-plan field instead of a literal 'rimsky-control-api' string match; the SQLite-plus-replicas warn-only gate riding the same typed machinery as the memory-blob hard gate. **DELIVERED**, confirmed 2026-07-20 (queued discovery, phase-3 step-4 dossier reconciliation): `persistence.Topology` (`lib/foundation/persistence/topology.go`) is a real typed string enum (`TopologyUnified` / `TopologySplit`) threaded through `config.OpenBlobBackend` and `persistence.SQLiteReplicaWarning`; `cmd/rimsky-entrypoint/main.go`'s `LaunchPlan.MigrateOwner` (typed bool, set via `Role.OwnsMigration()`) replaces the literal 'rimsky-control-api' string match.

## Intentional absences

- **Rimsky-side replica detection, heartbeat, failover, or addressing** — by definition of the concept (2026-05-17).
- **Advisory-lock-per-subscription / publisher HA machinery in rimsky** — rejected; HA is the publisher implementation's responsibility (2026-05-17, sensor-messaging-unification, explicitly superseding the post-data-platform-cleanup item-5 framing, which "was solving a problem that doesn't exist in the one-client-per-publisher-name architecture").
- **sensor-cron cross-replica advisory-lock coordination** — the pre-2026-05-17 draft promise is retired; the accuracy check enforces its absence (2026-06-06).
- **An in-repo @concept:replica annotation site** — recorded as having none during the 2026-05-25 backfill rather than fabricating one; the concept is deployment-posture prose.

## Corrections and restorations (drift-fight record)

- **Phantom advisory lock in sensor-cron** (2026-05-17, post-data-platform-cleanup): the doc comment claimed per-watch pg_try_advisory_lock guarding multi-replica deployments; the code had only an in-process sync.Mutex — the 2026-05-15-scoped feature was silently dropped and its multi-replica test never writable. Resolution arc: the same day's sensor-messaging-unification rejected the whole coordination approach, and 2026-06-06 retired the stale promise and pinned the honest N-fires posture with an accuracy check. Ruling precedent: the doc was fixed to match deliberate code posture, not the reverse.
- **False unified stamp** (2026-06-11): the all-in-one entrypoint spawned three child processes each stamped `RIMSKY_PROCESS_ROLE=unified` — never true (three processes cannot share an in-process blob map; the scheduler's orphan-blob sweep swept its own empty map). Fixed by making unified a genuine single-process path.

## Superseded / historical

- Post-data-platform-cleanup item 5 (multi-replica sensor coordination via advisory locks) and its intended exactly-one-fires behavior (2026-05-17 promise) → rejected the same day by sensor-messaging-unification's single-replica-by-design posture; fully retired 2026-06-06.
- All-in-one as three stamped child processes → one-process unified topology (2026-06-11).
