# Intent Dossier: publisher

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- A publisher is a peer service that pushes messages into rimsky; sensors are one kind of publisher (one whose message source is observation of external state). The protocol, proto service, verbs (Capabilities/Subscribe/Unsubscribe/ListSubscriptions), Go types, table (rimsky_publisher_subscriptions), and conformance vocabulary are all Publisher; "sensor" survives only as the bundled-services-layer name for the sensor implementations.
- Publishers send standard message envelopes to the generic messages endpoint; there is no special observation route and no server-side payload pre-shaping — downstream nodes read raw payload fields via substitution.
- A publisher service is a provider of broadcasters: one service process serves many instances; each subscription provisions a logical per-instance broadcaster parameterized by the instance's resolved config.
- Publisher subscriptions are desired-state rows with lifecycle mounting → active / failed / stopped: instance-create inserts mounting and returns fast; a reconciliation worker retries Subscribe forever with backoff; failed is reserved for non-retryable errors; the instance-detail surface exposes per-subscription state; startup resync (ResyncPublisherSubscriptions) is the durable safety net.
- Publisher state persistence lives in each publisher implementation (own table, own DB connection, env-var configured), never in foundation/persistence.
- Trust model v1 is network-perimeter: the publisher_subscription_id capability check stops trivial cross-instance spoofing; TLS config on publisher peer entries is honored at dial; mTLS is post-v1.
- Publishers participate fully in cross-cutting peer machinery: lifecycle-subscriber fan-out, validation roles, TLS — structural exclusions of publishers from peer enumerations have repeatedly been ruled bugs.

## Required behaviors (open promises)

- Protocol contract: a custom publisher implements Capabilities/Subscribe/Unsubscribe/ListSubscriptions, advertises message kinds with per-kind config schemas, accepts Subscribe with resolved per-instance config, emits via POST /instances/{id}/messages with the mandatory dedup header; after a rimsky restart, rimsky calls ListSubscriptions and reconciles to steady state without re-subscribing active subscriptions (2026-06-08, corpus-bootstrap, artifact).
- Desired-state subscription lifecycle (mounting/active/failed/stopped) with retry-forever reconciliation and per-subscription state visible on instance detail — a mount failure is never silent (2026-06-11, last-mile-stability, artifact): "instance-create inserts rows in `mounting` and returns; the instance-detail surface exposes per-subscription state."
- Publisher-side POST retry: ~3 attempts with exponential backoff on 5xx/connection errors, warn-and-abandon without advancing state so the next tick retries the same fire window; warn-and-drop on 403/404 (2026-05-17, sensor-messaging-unification, artifact) `(artifact-only)`.
- Messages from publishers persist with sender_kind 'publisher' and a sender derived from the publisher's authenticated identity, never from the request body (2026-06-02, acceptance-coverage-recovery, artifact). Sender discrimination is by auth path; the wire envelope carries no sender_kind (2026-06-19, 8a3b8c19, transcript — see api-key dossier).
- Publisher keys are narrow message senders (grant [{action:'message:send'}]); the messages handler validates publisher_subscription_id against caller identity in-handler (2026-05-15, control-plane-mcp-and-auth, artifact).
- Startup resync invoked for real: ResyncPublisherSubscriptions runs at control-api startup, re-issuing dropped subscriptions for live instances and stopping orphans (2026-06-02, rimsky-core-remediation, artifact — was dead code before).
- Lifecycle fan-out includes publishers: peersReferencedBySpec walks spec.Publishers so a publisher/sensor declaring lifecycle-subscriber receives lifecycle events; lifecycle is universal across peer kinds with slot vs protocol orthogonal; regression-tested (2026-06-19, 08d65bfe + 8a3b8c19, transcript).
- Validation mix-in honored for publisher peers identically to claim producers; the validation pipeline keeps the kind-shaped role string 'sensor' when probing validation peers — a deliberate, spec-aligned exception to the rename (2026-05-17 divergences + 2026-06-11, artifact).
- Pause does not silence publishers: their messages write to rimsky_messages normally, accumulate as pending, and drain per frame_delivery_mode after resume (2026-05-24, instance-debugger, artifact).
- Cross-stack e2e proofs for the bundled sensors (cron/http/webhook/object-store plus the publisher example) exist and pass as integrated-stack testcontainers proofs — in-process fakes cannot substitute for binaries that ship separately (2026-06-15, 91ec93d1, transcript, user).
- tls config on publisher peer entries honored at the publisher dial site (2026-06-11, last-mile-stability, artifact).

## Intentional absences

- The sensor-observation route POST /sensors/{watch_id}/observations and its handler: deleted; a message is a message (2026-05-17, sensor-messaging-unification, artifact).
- payload_template pre-shaping (and OnObservationSpec): dropped; publishers send raw observation bytes; templates carrying payload_template fail as a standard unknown-field error, not a bespoke message (2026-05-17, artifact).
- Multi-replica publisher coordination in rimsky (advisory-lock-per-subscription, heartbeat protocol, periodic drift resync): deliberately not rimsky's concern; HA is the publisher implementation's responsibility; bundled sensors declare single-replica models (2026-05-17, artifact, superseding post-data-platform-cleanup item 5).
- Publisher state in foundation/persistence or a shared sensors/polling library: declined; each sensor owns its own table and connection via RIMSKY_SENSOR_<KIND>_STATE_DSN, no rimsky.yml key (2026-05-17, artifact).
- Bounded Subscribe retry (3 attempts then failed) and synchronous inline Subscribe at instance-create: abandoned in favor of the retry-forever reconciliation worker; failed means non-retryable only (2026-06-11, last-mile-stability, artifact).
- A separate sensor:observe / publisher:observe permission action: never existed by design (2026-05-15, artifact).
- Standalone sensor as a protocol primitive: retired; sensor vocabulary in proto/validation/conformance/SDK surfaces (SensorContext, sensor_name, ValidateSensor, role string 'sensor' at the rimsky boundary, conformance SensorHappy) was residue and swept to Publisher forms — except the validation kind-role noted above and bundled instance names like sensor-cron, which are legitimate publisher-instance names (2026-06-19, 08d65bfe, transcript).

## Corrections and restorations (drift-fight record)

- The standalone rimsky-control-api binary never wired the parsed publishers: block into its dependencies — empty registry, every subscription failing unknown_publisher in three-container splits while all-in-one worked; a two-construction-paths drift, closed in the binary (2026-06-02, acceptance-coverage-recovery, artifact). Precedent: parallel construction paths must be kept in parity.
- ResyncPublisherSubscriptions documented as running at startup but had zero call sites; wired at control-api startup (the spec had drafted supervisor startup) and every doc string repeating the stale claim corrected (2026-06-02, rimsky-core-remediation, artifact).
- The five cross-stack e2e sensor/publisher proofs deleted during the message-schema plan were ordered restored and mechanically patched to the new wire shape, not rewritten or abandoned (2026-06-15, 91ec93d1, transcript, user: "crasy to throw them out … when all that changed was the message shape").
- Lifecycle fan-out structurally excluding publishers (peersReferencedBySpec hardcoding stores + executors) was ruled "clearly a bug" and fixed with a regression test; concept:lifecycle-subscriber updated to state lifecycle is universal across peer kinds (2026-06-19, transcript).
- The sensor→publisher rename left residue in validation/conformance/SDK surfaces for a month; completed 2026-06-19 with proto field numbers preserved (transcript).

## Superseded / historical

- 'Sensor' as the protocol-level abstraction → Publisher protocol; sensors are one kind of publisher (2026-05-17, artifact).
- sensors: template block → publishers:; old key rejected with a pre-v1 rename error (2026-05-17, artifact).
- Wire sender_kind enum value 'sensor' → 'publisher' (2026-05-17); then sender_kind dropped from the wire entirely, stamped server-side from auth (2026-06-19, transcript).
- Bounded (3-attempt) Subscribe retry → retry-forever reconciliation worker (2026-06-11).
- Proxy Publisher handler shipped as registered gRPC service returning UNIMPLEMENTED pending follow-up wiring (2026-05-24, host-agent-and-proxy, artifact); the corpus-bootstrap promise that host-agent late-binding covers all five protocols with no Unimplemented stub (2026-06-08) supersedes it as the target state.
