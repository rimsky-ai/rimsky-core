---
audit: replica
artifact: concept:replica
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Rimsky does not model replicas, with the scheduler tick as the single named exception

Supported. Sweeping the whole non-test tree for any notion of a replica turns up exactly three sites, and all three agree with the concept: the scheduler's dispatch tick, which takes the persistence layer's scheduler-tick advisory lock and skips the pass when another process holds it; the topology helper; and the SQLite warning it feeds. There is no replica registry, no heartbeat, no per-replica addressing, and no generic replica-aware coordination anywhere in the runtime, which is the concept's central negative claim. The topology type admits exactly two values, unified and split, derived from a single process-role marker with no replica-count signal of any kind, and the all-in-one path sets the unified value while every per-role process falls through to split. Choosing the SQLite driver outside the unified topology returns a warning string naming the shared-local-file precondition and is collected as a warning, never an error, so startup proceeds; the config suite covers all four driver-by-topology combinations. Of the four bundled sensor binaries none attempts cross-replica coordination, and the cron sensor's two replica suites assert the honest posture directly: one replica fires once per window, and two replicas each fire, colliding only in the control API's idempotency dedup. Cross-replica-consistent control API behaviour rests on database transactions rather than any rimsky-level coordinator.
