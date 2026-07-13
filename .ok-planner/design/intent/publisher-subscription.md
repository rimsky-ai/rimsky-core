# Intent Dossier: publisher-subscription

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- A publisher-subscription is a publisher-to-rimsky binding for one instance; a publisher service is a provider of broadcasters — one process serves many instances, each subscription provisioning a logical per-instance broadcaster from resolved config (2026-06-11). Orthogonal to node-subscription (a node's declared wait on a sibling), which was renamed away from bare "subscription" precisely to keep the two distinct (2026-05-17).
- Subscriptions are **desired-state rows** with lifecycle `mounting → active | failed | stopped`: instance-create inserts `mounting` and returns fast; a reconciliation worker drives Subscribe with backoff and **no attempt cap**; `failed` is reserved for non-retryable errors; per-subscription state is visible on the instance-detail surface (2026-06-11).
- The `publisher_subscription_id` is a capability token minted by rimsky at Subscribe time; the messages handler validates it against the active row (instance-scoped) and derives the sender from the authoritative row — publishers cannot spoof.
- Routing is **purely by message_type** matched against node-subscription edges. `target_node` is gone end-to-end (drop migration 014); `message_kind` was renamed `message_type` with the `invalidate` default retired, validated against the target instance's message registry (2026-06-14/2026-06-19, transcript).
- There is no mid-subscription reconfiguration verb: template publisher-block changes flow through a new instance and a new subscription id (2026-05-17, never retracted).
- Instance termination reads **nothing** about publisher-subscriptions (2026-06-03 invariant).

## Required behaviors (open promises)

- POST /instances/{id}/messages under the publisher capability requires `publisher_subscription_id`; rimsky resolves it against an active row scoped to that instance and rejects unknown id / cross-instance mismatch / stopped-or-failed subscription with 403, missing id with 400; the sender is overwritten from the authoritative row's `publisher_name` (2026-05-17, sensor-messaging-unification, artifact): "don't trust sender from request; derive from authoritative row." Reaffirmed as an acceptance promise 2026-06-02.
- The persisted message row carries the derived sender and a persistence-layer `sender_kind` stamped from the auth context — `sender_kind` is not on the wire; the authentication path (operator API key vs publisher-subscription capability) is the discriminator (2026-06-19, 8a3b8c19, transcript): "if the auth is different, doesn't that implicitly differentiate?"
- Message delivery routes purely by `message_type` matched against node-subscription edges (2026-06-19, a02fe167, transcript, user): "rip it our and update the design doc… just fix it." No per-subscription target-node routing exists.
- `message_type` on the subscription is validated against the target instance's message registry; the legacy single-valued `invalidate` kind is retired (2026-06-14, bfc9febb, transcript).
- Desired-state lifecycle: `mounting/active/failed/stopped`; reconciliation worker retries mounting subscriptions forever with backoff; `failed` only for non-retryable errors (e.g. publisher name not in registry); `stopped` on unsubscribe; instance-detail exposes per-subscription state so a mount failure is never silent (2026-06-11, last-mile-stability, artifact): "retry-forever matches desired-state semantics; bounded budgets convert contention spikes into silent failures."
- Startup resync: rimsky reconciles subscriptions at startup — re-issuing dropped subscriptions for live instances and stopping orphans; landed at control-api startup (2026-06-02, rimsky-core-remediation, artifact). Reconciliation uses the publisher's `ListSubscriptions` and does not re-subscribe what is already active (2026-06-08, corpus-bootstrap, artifact).
- Publisher protocol surface: Capabilities/Subscribe/Unsubscribe/ListSubscriptions, per-kind config schemas, resolved per-instance config at Subscribe, emission via POST with the mandatory dedup header (2026-06-08, corpus-bootstrap, artifact).
- Publisher POST retry: ~3 attempts on 5xx/connection errors with exponential backoff, warn-log and abandon without advancing state so the next tick retries the same fire window; on 403/404 warn-log and drop the observation (2026-05-17, sensor-messaging-unification, artifact-only).
- Control-api tests pin every idempotency and publisher-capability outcome to its exact HTTP status (first-insert 201; same-key replay 200 with identical message_id; missing key 400; active subscription success; stopped 403; unknown 403/400; wrong-instance 403; missing subscription id 400) so any single-status regression fails the build (2026-06-06, comprehensive-gap-closure, artifact-only; statuses touched by the 2026-06-19 sender_kind wire removal should be judged against that later decision).
- Bundled sensors authenticate as narrow message senders with grant `[{action:'message:send'}]`; there is no separate observe action — the messages handler's subscription-id check is the capability check (2026-05-15, control-plane-mcp-and-auth, artifact-only).
- Observability of publisher activity is a message-table query (`GET /instances/{id}/messages?sender=<publisher_name>`, `received_at` as timestamp) — not per-subscription state (2026-05-17).
- sensor-cron persists active subscriptions and next-fire watermarks to a durable state DB when `RIMSKY_SENSOR_CRON_STATE_DSN` is set, surviving restart and firing the originally-scheduled window (advancement from the row's prior `next_fire_at`, not wall clock); unset keeps the in-memory default relying on Subscribe replay (2026-06-06 + 2026-06-08, artifact).

## Intentional absences

- **`target_node` on the subscription** — removed end-to-end (proto, YAML PublisherSpec, validator checks, DB column via drop migration 014 in both drivers, sensor state DBs, conformance runner, concept docs). It was dead routing — required at registration, never read by delivery — and pinned a single receiver when the model supports many (2026-06-19, a02fe167 + 8a3b8c19, transcript, user-ordered).
- **`sender_kind` on the wire envelope** — dropped; auth path discriminates. The persistence enum keeps all three values (operator, publisher, instance) for audit; `instance` is stamped only by the runtime cascade-emit path and never appears on the wire (2026-06-19, 8a3b8c19, transcript).
- **`message_kind` with default `invalidate`** — renamed `message_type`, default retired (2026-06-14, transcript).
- **Per-subscription `last_observed_at`** — deliberately dropped 2026-05-17; observability via the message table.
- **Bounded (3-attempt) Subscribe retry before flipping the row to `failed`, and the synchronous inline Subscribe at instance-create** — abandoned 2026-06-11 for the mounting-state reconciliation worker with retry-forever.
- **A mid-subscription reconfiguration verb** — deliberately never built (2026-05-17).
- **Coupling of instance termination to active publisher-subscriptions** — the NOT-EXISTS clause added by acceptance-coverage was explicitly reverted (2026-06-03); termination reads nothing about subscriptions.
- **A separate `sensor:observe` / `publisher:observe` auth action** — never existed by design (2026-05-15).

## Corrections and restorations (drift-fight record)

- **ResyncPublisherSubscriptions dead code** (2026-06-02, rimsky-core-remediation): documented as running at supervisor startup, zero call sites — subscriptions never reconciled after restart. Fixed by invoking it at control-api startup (deliberate divergence from the spec's "supervisor startup"), and every doc string repeating the stale claim was corrected.
- **Sensor-watched instance died on first settle** (2026-06-02, acceptance-coverage-recovery): the terminal predicate gained a NOT-EXISTS over active subscriptions so the next sensor emit didn't 409… then **that fix itself was ruled drift** (2026-06-03, instance-lifecycle-durable-by-default) — it bound termination to sensors, exactly the coupling the durable model forbids — and was reverted. Precedent: a symptom patch that violates an independence invariant gets reverted even though it fixed a real bug; the durable-instance model is the fix.

## Superseded / historical

- `target_node` + `message_kind` as inline routing scalars on SubscribeRequest, set once at Subscribe (2026-05-17) → `target_node` removed entirely; `message_kind` → `message_type` (2026-06-14/19).
- Wire `sender_kind` discriminator (2026-05-17) → auth-path discrimination, persistence-only stamp (2026-06-19).
- 3-retry Subscribe then `failed` + synchronous Subscribe at instance-create (2026-05-17) → mounting state + retry-forever reconciliation worker (2026-06-11).
- Termination gated on active subscriptions (2026-06-02) → termination fully independent (2026-06-03).
