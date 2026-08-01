# Intent Dossier: sensor

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- A sensor is a class of Publisher implementation whose message source is observation of external state. The protocol is Publisher (Subscribe / Unsubscribe / ListSubscriptions, rimsky_publisher_subscriptions); "sensor" survives only as the bundled-services-layer name because the bundled implementations ARE sensors (2026-05-17, sensor-messaging-unification, artifact).
- Four bundled sensors exist: sensor-cron, sensor-http, sensor-object-store, sensor-webhook, shipped as deployable images (2026-05-15 / 2026-05-17, artifact).
- Cron is not a rimsky-core capability: cron nodes were replaced by the bundled cron sensor publishing via the messages endpoint like any other publisher; no cron/tick runtime type-path exists or should exist (2026-05-15 reversal, data-platform-extensions, artifact; confirmed 2026-06-15, 91ec93d1, transcript).
- Message delivery routes purely by message_type matched against node-subscription edges — there is no per-subscription target-node routing (2026-06-19, a02fe167, transcript).
- Replica posture is honest single-replica: one replica fires each window once; N independent replicas fire N times; rimsky claims no cross-replica coordination (2026-06-06, comprehensive-gap-closure, artifact).
- Sensor state persistence lives in each sensor implementation (own table, own DB connection, RIMSKY_SENSOR_<KIND>_STATE_DSN env var only) — never in foundation/persistence, never via a rimsky.yml key (2026-05-17, sensor-messaging-unification, artifact).
- Sensors are lifecycle-fan-out peers like any referenced service; slot vs protocol are orthogonal (2026-06-19, 08d65bfe, transcript).
- Sensors are external standalone services only — not part of the in-process bundling scope (2026-07-03, 8a8539a4, transcript).
- Instance termination reads nothing about sensors or publisher-subscriptions (2026-06-03, instance-lifecycle-durable-by-default, artifact).

## Required behaviors (open promises)

- The headline reactive claim: a real bundled sensor observing external state drives the full path — a change in the watched endpoint produces a persisted message row with sender_kind publisher, and the subscribing downstream node transitions stale then re-runs to fresh through the cascade (2026-06-02, acceptance-coverage-recovery, artifact).
- Sensors continue to work exactly as they did before the message-schema layer; the only change absorbed is the new message format. "sensors should continue to work exactly as they did before, just with the new message format" (2026-06-15, 91ec93d1, transcript).
- The five cross-stack e2e proofs (cron / http / webhook / object-store scenarios plus the publisher example e2e) exist, are patched to the current wire shape, and pass; in-process fakes cannot substitute for them because bundled sensors ship as separate binaries. "crasy to throw them out … when all that changes was the message shape" (2026-06-15, 91ec93d1, transcript).
- sensor-cron: missed fires are NOT backfilled (missed_fires: drop); next_fire_at advances from the prior next_fire_at, not wall clock, so a long outage yields exactly one post-outage fire (2026-05-15, data-platform-extensions; 2026-06-08, corpus-bootstrap, artifact).
- sensor-cron: RIMSKY_SENSOR_CRON_STATE_DSN, when set, persists active cron publisher-subscriptions and next-fire watermarks to Postgres and survives restart firing on the originally-scheduled window with no external re-subscribe; empty/unset keeps the in-memory default with recovery via Subscribe replay (2026-06-06, comprehensive-gap-closure; 2026-06-08, corpus-bootstrap, artifact).
- sensor-http polls a URL at a fixed interval, emits one message per tick on success with an optional response-body filter, and persists its body hash across restart (2026-05-17; 2026-06-08, artifact).
- sensor-webhook acknowledges an inbound POST only after rimsky has persisted the translated message, and persists per-subscription idempotency-key history with TTL (2026-05-17; 2026-06-08, artifact).
- sensor-webhook requires per-subscription inbound authentication — exactly one of `hmac` (HMAC-SHA256 over the raw body, optional timestamp header + replay window), `secret_header` (constant-time header compare), or an explicit `none` — fail-loud: a subscription with no `auth` block is refused at bind time, so the insecure `none` mode must be typed deliberately (mirroring the egress guard's closed-by-default polarity). Closes unauthenticated message injection and forged-idempotency-key pre-seeding on the public-web ingress (2026-07-16, peer-auth-posture, transcript; see the peer-auth dossier).
- sensor-object-store emits exactly one message per newly-discovered object, persists its watermark cursor so restarts never re-emit (watermark advances before the POST — at-most-once), and has pluggable backend listers registered via SetBackend (2026-05-15; 2026-06-08, artifact).
- sensor-object-store rejects backend s3/gcs/azure at Subscribe with a clear error and drops them from Capabilities — advertise only what the binary services (2026-06-02, rimsky-core-remediation, artifact).
- Bundled publishers generate an idempotency key per fire: cron = subscription id + fire-window ISO timestamp; http = subscription id + body SHA-256; object-store = subscription id + object etag; webhook = subscription id + incoming idempotency header (2026-05-17, sensor-messaging-unification, artifact-only).
- Bundled sensors authenticate as narrow message senders (grant [{action:'message:send'}]); the messages handler validates publisher_subscription_id against caller identity inside the handler (2026-05-15, control-plane-mcp-and-auth, artifact-only).
- Sensors/publishers declaring lifecycle-subscriber receive lifecycle events: the fan-out peer set unions publisher references (regression-tested) (2026-06-19, 08d65bfe / 8a3b8c19, transcript).
- The four sensors ship as deployable artifacts: per-binary Dockerfiles, image-build entries, compose services, helm deployments with per-sensor enable flags, default replicas 1 (2026-05-17, sensor-messaging-unification, artifact-only).

## Intentional absences

- Per-node schedule: field, rimsky_schedules table, the scheduler cron-fire path, concept:schedule, and the force-fire admin endpoint/CLI/MCP tool — all retired when cron moved to sensor-cron (2026-05-15, data-platform-extensions, artifact; reaffirmed 2026-06-15, 91ec93d1, transcript).
- Rimsky-side multi-replica publisher coordination: no advisory-lock-per-subscription primitives, no heartbeat protocol, no periodic resync-to-detect-drift. HA is the publisher implementation's responsibility (2026-05-17, sensor-messaging-unification, artifact; posture pinned with an accuracy check asserting no advisory-lock/leader-election primitive in sensor-cron source, 2026-06-06, comprehensive-gap-closure, artifact).
- sensor-sql (surface too complex) and sensor-kafka (heavy dependency) — explicitly not bundled (2026-05-15, data-platform-extensions, artifact).
- s3/gcs/azure backend implementations in the bundled object-store sensor — deliberately cut; operators register their own via SetBackend (2026-06-02, rimsky-core-remediation, artifact).
- target_node on publisher subscriptions — dead routing removed end-to-end (proto, template PublisherSpec, validator checks, DB column via migration 014, runtime, sensor state DBs, conformance, concept docs). "rip it our and update the design doc" (2026-06-19, a02fe167, transcript).
- rimsky.yml key for sensor state DBs, foundation-hosted publisher state, and a shared sensors/polling library — all declined; forced sharing would create artificial coupling (2026-05-17, sensor-messaging-unification, artifact).
- In-process bundling of sensors — dropped from scope; only claim producers and executors are bundled in-proc. "let's drop sensors and listeners. agreed." (2026-07-03, 8a8539a4, transcript)
- A separate sensor:observe / publisher:observe permission action (2026-05-15, control-plane-mcp-and-auth, artifact-only).
- Coupling instance termination to active publisher-subscriptions — explicitly reverted; termination is independent of sensors (2026-06-03, instance-lifecycle-durable-by-default, artifact).

## Corrections and restorations (drift-fight record)

- sensor-cron's documented advisory-lock multi-replica guard never existed in code (doc comment claimed pg_try_advisory_lock; only an in-process mutex) — resolved by retiring the promise and pinning the honest single-replica contract (fireCount==2 for two replicas) with an accuracy check (2026-05-17, post-data-platform-cleanup; 2026-06-06, comprehensive-gap-closure, artifact).
- The supervisor-startup ResyncSensorWatches hook was never wired (helper existed, uncalled) (2026-05-15, data-platform-extensions, artifact) — the whole watch-resync surface was then superseded by the publisher model's no-resync posture (2026-05-17).
- sensor-cron's state-DB module / RIMSKY_SENSOR_CRON_STATE_DSN plumbing was skipped during execution despite plan direction (2026-05-17, sensor-messaging-unification-divergences, artifact) — re-promised and specified 2026-06-06 (see Required behaviors); adjudicators should verify delivery.
- The message-schema plan deleted the five cross-stack sensor/publisher e2e proofs; the user ordered them restored and mechanically patched, not rewritten — restored and passing (2026-06-15, 91ec93d1, transcript).
- Lifecycle fan-out silently excluded publishers/sensors (peersReferencedBySpec enumerated only claim-producer and executor slots) — fixed to union publisher references, with regression test (2026-06-19, 08d65bfe / 8a3b8c19, transcript).
- Retired sensor-primitive vocabulary residue swept from validation/conformance/SDK: SensorContext→PublisherContext, sensor_name→publisher_name (field number preserved), ValidateSensor*→Publisher* forms, role string sensor→publisher, SensorHappy→PublisherHappy. Bundled instance-name values like sensor-cron remain — those are publisher-instance names (2026-06-19, 08d65bfe / 8a3b8c19, transcript).
- sensor-webhook StopWatch: chi cannot unregister routes, so a stopped watch's route stays mounted returning 404 — documented pre-v1 limitation flagged for a router follow-up (2026-05-15, data-platform-extensions, artifact-only).
- The sensor concept doc was rewritten end-to-end (not patched) to the publisher-class definition, dropping aspirational advisory-lock/multi-replica language (2026-05-17, sensor-messaging-unification, artifact).

## Superseded / historical

- Sensor as its own service kind with StartWatch / StopWatch / ListWatches, POST /sensors/{watch_id}/observations, and rimsky_sensor_watches (2026-05-15) — superseded by the Publisher protocol unification (2026-05-17): Subscribe/Unsubscribe/ListSubscriptions, rimsky_publisher_subscriptions, instance-scoped message path.
- The ListWatches-based resync-after-restart promise (2026-05-15) — superseded by the no-resync rejection (2026-05-17); recovery is the publisher's own persistence plus Subscribe replay.
- Multi-replica advisory-lock serialization promise (2026-05-15 scope; 2026-05-17 cleanup design) — retired in favor of the honest single-replica posture (2026-05-17 unification; 2026-06-06).
- The deliberate 'sensor' role string on the Validation protocol (2026-05-17, artifact) — superseded by the transcript-tier sweep making the role discriminator publisher everywhere (2026-06-19).
- payload_template pre-shaping on watches — dropped; publishers send raw bytes, receivers substitute at dispatch (2026-05-17, sensor-messaging-unification, artifact).
- (target_node, message_type) routing — superseded by message_type-only routing (2026-06-19).
- Termination gated on active publisher-subscription (acceptance-coverage symptom fix) — reverted (2026-06-03).

## Conflicts needing human ruling

None recorded — the precedence rules resolve the record's tensions on this concept.
