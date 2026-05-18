# Publisher Protocol Unification + Deploy Surface Design

**Date:** 2026-05-17
**Status:** Draft (rewritten 2026-05-17 to supersede prior cycles)
**Supersedes:** Item 5 of `.ok-planner/history/plans/2026-05-17-post-data-platform-cleanup.md` (which targeted multi-replica sensor coordination but landed only a pin test because the underlying architecture was wrong for the problem).

## Preamble — supersession lineage

This spec is the rewritten end-to-end design that supersedes the three earlier review-cycle drafts of the "sensor messaging unification" spec. The earlier drafts were internally consistent but were written before two architectural decisions landed in a subsequent design discussion:

1. **The migration-flatten housekeeping pass collapsed migrations 002-010 into rewritten baselines.** With staged-schema migration discipline removed, the original 5-stage plan (which existed to keep each migration shippable) collapses to 3 stages. Schema changes now land via direct edits to `001-baseline.sql` rather than enumerated `00N-…` migrations.

2. **The "Watch → Subscription" rename was reframed as the "Publisher rename".** The earlier draft deferred the rename to a follow-up pass. The design-discussion concluded that the protocol-level name is wrong everywhere it appears: rimsky has *publishers* (peer services that publish messages into rimsky); sensors are one kind of publisher. The protocol is `Publisher`. `Sensor` stays as the bundled-services-layer name (the same shape as `ClaimProducer` the protocol vs `Store` the bundled-services layer). The rename is folded into this spec rather than deferred — it is the architectural change.

The net effect: most of this spec's work is removal of wrong abstractions. The rimsky-side observation route, the substitution machinery, the `on_observation` column, the `OnObservationSpec` Go type, the "Sensor protocol" + "Watch" misnomers — all subtraction. The net code change is negative. The publisher rename is the largest mechanical surface (~50-60 files touched) but does not add new abstractions; it renames existing ones to honest names.

## Goal

Three interleaved concerns:

1. **Collapse the sensor observation pipeline into the existing message pipeline.** Today rimsky exposes a special `route:POST /sensors/{watch_id}/observations` endpoint that does watch-row lookup + payload-template substitution + observability bookkeeping before inserting into `table:rimsky_messages`. None of this is sensor-specific work — it's wrapper logic that the generic `route:POST /instances/{id}/messages` endpoint can absorb after small additions. The wrapper exists because the sensor protocol evolved separately from the messaging API; the architectural review during this session concluded the separation isn't earning its keep.

2. **Rename "Sensor protocol" → "Publisher protocol", "Watch" → "PublisherSubscription".** The protocol-level abstraction in rimsky is "a peer service that publishes messages into rimsky." Sensors are one kind of publisher (publishers that observe external state). The protocol, the proto file, the verbs (`Subscribe` / `Unsubscribe` / `ListSubscriptions`), the Go types (`PublisherRegistry`, `PublisherClient`, `PublisherSubscriptionRow`), the schema table (`rimsky_publisher_subscriptions`), the conformance binary (`rimsky-publisher-conformance`), and the concept docs all use the publisher / publisher-subscription vocabulary. The bundled implementations stay under `pkg:sensors/sensor-*/` — they ARE sensors; the rename is to the protocol they implement, not the services themselves.

**Concept naming note (cycle-4 resolution):** the publisher-side concept is `concept:publisher-subscription` (not `concept:subscription`), because `concept:subscription` is already taken by the template-DSL receiver-side `subscribes:` block. To prevent collision, the existing template-DSL concept renames `concept:subscription` → `concept:node-subscription` as part of this spec's concept-doc surgery. The two concepts are orthogonal: `concept:publisher-subscription` is a publisher↔rimsky binding (one publisher subscribes to publish messages for one instance); `concept:node-subscription` is a template-DSL block that declares a node's receiver-side subscription to a sibling's terminal-changed signal.

3. **Ship the four bundled sensors as deployable artifacts.** `pkg:sensors/sensor-cron`, `pkg:sensors/sensor-http`, `pkg:sensors/sensor-object-store`, `pkg:sensors/sensor-webhook` exist in source but `file:deploy/build-images.sh` doesn't build them, `file:deploy/docker-compose.yml` doesn't run them, and the helm chart has no values for them. They're testable via `cmd:go test ./sensors/...` but not deployable through any standard rimsky stack.

Plus the surrounding correctness work: three bundled sensors keep non-reconstructible state in memory and silently re-emit observations on restart; the universal messaging endpoint has no idempotency story; concept docs claim multi-replica behavior the code doesn't deliver.

**Subtraction not addition.** Most of this spec is removal: the rimsky-side observation route, the substitution machinery, the `on_observation` column, the `last_observed_at` column, the `OnObservationSpec` Go type, the "Sensor protocol" misnomer, the "Watch" misnomer. The unification works because the generic messages endpoint already does almost everything the observation route did. The publisher rename works because the new names are the right names — they describe what the protocol always was. The net code change is negative.

## Architectural shape after this spec lands

**Rimsky's contract with publishers collapses to four verbs + one HTTP route**:

- `proto:publisher.proto::Capabilities` — publisher advertises kinds + optional protocols.
- `proto:publisher.proto::Subscribe(publisher_subscription_id, instance_id, kind, resolved_config, target_node, message_kind)` — rimsky tells a publisher "start publishing for this publisher-subscription; here's where to route messages." Inline routing fields (no substruct).
- `proto:publisher.proto::Unsubscribe(publisher_subscription_id)` — rimsky tells a publisher "stop publishing for this publisher-subscription."
- `proto:publisher.proto::ListSubscriptions` — reconcile-on-startup verb.
- `route:POST /instances/{id}/messages` (existing generic endpoint) — publishers POST messages as standard envelopes with `sender_kind: "publisher"`, `publisher_subscription_id` (capability check), and `payload: <raw bytes>`.

**What rimsky no longer does**:

- No special observation route. Removed.
- No payload-template substitution at observation time. Removed. The existing graph attribute substitution (`code:graph/attribute/substitution.go::walkPath` / `code:graph/attribute/substitution.go::resolveTrigger`) handles `{{trigger.message.payload.<path>}}` references at dispatch time, treating payload bytes as inert per `@blessed-invariant 21`.
- No publisher-subscription-level `last_observed_at` tracking. Operators query `route:GET /instances/{id}/messages?sender=...` for observability; `col:rimsky_messages.received_at` is the timestamp.
- No multi-replica coordination, periodic resync, heartbeat tracking, or advisory-lock primitives for publishers. The reference impls declare single-replica deployment models in their docs and back that with state-persistence to survive restart.

**What the publisher implementer is responsible for**:

- Watching/observing/listening to whatever external thing the publisher is for (cron clock, HTTP endpoint, object-store prefix, webhook port — for the four bundled sensors).
- Holding per-publisher-subscription state. Persisting it durably if the state is non-reconstructible (sensor-http's body-hash, sensor-object-store's cursor, sensor-webhook's idempotency-key history). sensor-cron's state IS reconstructible (`sched.Next(now)`); persistence is optional.
- Multi-replica HA, if the publisher supports it. For v1 the four bundled sensors all declare single-replica.
- Building message envelopes from the routing fields received at `Subscribe` and POSTing to `route:POST /instances/{instance_id}/messages`.

## Tensions resolved

These were the open architectural questions resolved during the inline design discussion. Each is documented as a `concept:` boundary change.

### Tension 1: Should multi-replica safety live in rimsky or in the publisher?

**Resolution: in the publisher implementation, not in rimsky.** The current architecture (one gRPC client per publisher name; gRPC `pick_first` LB; one replica active per name) means multi-replica = "operator can scale; replicas other than the LB-picked one are idle." The "advisory lock per subscription" framing from the earlier plan was solving a problem that doesn't exist in this architecture (two replicas double-firing). The real shape is: if an operator wants HA, they pick a publisher implementation that handles it. Reference impls all declare single-replica + restart-on-fail. Closing this question per `concept:replica` — replica count is a deployment-tier knob; rimsky's runtime doesn't model replicas.

### Tension 2: Should state persistence live in `foundation/persistence` or in each publisher?

**Resolution: in each publisher.** `foundation/persistence` is project-agnostic primitives (per the `code:.golangci.yml` depguard `foundation-purity` rule). Publisher state shapes are all different (NextFireAt, body-hash, watermark-cursor, idempotency-key). Forcing a shared schema would bend foundation into a publisher catalog. Each publisher owns its own table in its own DB connection, mirroring how `pkg:stores/postgres` already manages its own ledger.

### Tension 3: Should the observation deposit endpoint be publisher-specific or unified with the message endpoint?

**Resolution: unified.** A message is a message. The special route was duplicating the generic endpoint's work plus three things that move cleanly: (1) routing config moves to `Subscribe` payload as inline fields; (2) substitution moves to dispatch-time attribute layer (already exists); (3) `last_observed_at` is replaced by message-table queries. The generic endpoint accepts `sender_kind: "publisher"` with `publisher_subscription_id` capability validation.

### Tension 4: Should the protocol carry the message payload-template?

**Resolution: no — drop the template.** The publisher sends the raw observation/event/payload as the message payload. Any downstream interpretation happens via the existing `code:graph/attribute/substitution.go::resolveTrigger` machinery at dispatch time, which already reads `{{trigger.message.payload.<field>}}` and treats payload bytes as inert (`@blessed-invariant 21`). The pre-shaping convenience of `payload_template` doesn't pay for its complexity. Templates that include `payload_template` are hard-rejected at registration with a clear error message pointing operators at the substitution path.

### Tension 5: Should idempotency live at the observation endpoint or at the message endpoint?

**Resolution: at the message endpoint, universally.** Operators benefit (retries don't create duplicate invalidates). Publishers benefit (replay-safe). Backfill flows benefit. The idempotency primitive is a property of the message-create surface, not publisher-specific.

### Tension 6: `Sensor protocol / Watch` → `Publisher protocol / PublisherSubscription` rename in this spec?

**Resolution: yes — folded into this spec.** Earlier drafts deferred it. The reframe — protocol-level abstraction is "a peer that publishes messages"; sensors are one kind of publisher — clarifies that the names were wrong, not just inelegant. The rename touches proto + generated bindings + table + route + concept docs + four sensor binaries + tests + conformance binary + CLAUDE.md. Atomic with the architectural change because both target the same surfaces; doing them sequentially would force two passes over the same files. The bundled implementations stay under `pkg:sensors/sensor-*/` (they ARE sensors); the rename is to the protocol they implement.

The new publisher-side concept is named `concept:publisher-subscription` (not `concept:subscription`); the existing receiver-side template-DSL concept `concept:subscription` is renamed to `concept:node-subscription` to clear the collision. Both renames are part of this spec's concept-doc surgery — see §Concept doc changes.

### Tension 7: Should Stage 2 (publisher unification) be incremental or atomic?

**Resolution: atomic.** Pre-rewrite drafts split the protocol rename, the observation-route drop, the `on_observation` column drop, and the proto regeneration across several stages to preserve mid-stage build cleanliness. Migration-flatten housekeeping removed the staged-schema motivation; without that, incrementality is solving for nothing. The architectural change is a single coherent unification, and forcing it through 4 partial-state intermediate stages would create more churn than landing it once. The `010` migration-flatten baseline rewrite is the reference shape: pre-v1 break-freely, edit the baseline, dev DBs get wiped.

## Pre-existing `payload_template` usage survey

Source survey (cycle-1 review Issue 5): `rg 'payload_template|PayloadTemplate' --type=go --type=yaml --type=md` returns the following hits, all of which sit inside the rimsky-side observation-deposit wrapper and translate cleanly to the unified design:

- `code:control/controlapi/sensors.go:18` (handler docstring), `:25` (comment), `:71` (request-body struct field), `:127-133` (handler invocation), `:191-217` (the `substituteObservationTemplate` helper itself), `:205-206` (the in-handler comment describing the no-template default behavior). All of this is the observation-deposit route's body — the entire file is deleted in Stage 2.
- `code:foundation/spec/graphs.go:88-99` — the `OnObservationSpec` struct declaring `PayloadTemplate map[string]any`. The entire struct is deleted in Stage 2; the routing fields it carried (`TargetNode`, `MessageKind`) are inlined directly into `proto:publisher.proto::SubscribeRequest`, eliminating the Go substruct.
- `code:graph/node/template.go:58` — the `type OnObservationSpec = spec.OnObservationSpec` alias. Deleted alongside the underlying type.
- `code:control/controlapi/sensors_test.go` — the `payload_template` key + value live at `:185-187` inside the surrounding `on_observation:` block at `:175-189`. Entire file deleted alongside `sensors.go` in Stage 2.

**Honest migration assessment**: every existing `payload_template` consumer is an internal piece of the observation-deposit machinery, not a downstream operator-facing shape. There are no operator templates registered against rimsky that read `payload_template` indirectly — the substitution runs server-side at deposit time and the substituted payload becomes the `rimsky_messages.payload` bytes. After this spec lands, downstream consumers read the raw observation bytes via `{{trigger.message.payload.<field>}}`; for the small set of `payload_template`s in existing test fixtures the conversion is mechanical (rewrite the receiver-node's attribute `source` directives to reach into the raw observation shape rather than the pre-shaped synthetic shape). Templates carrying `payload_template` are rejected at registration with: `"payload_template removed; raw observation bytes are passed through as message payload. Read fields via {{trigger.message.payload.<path>}} at the consuming node."`

## File map

### Created

- `sensors/sensor-cron/Dockerfile.sensor-cron` — bundled sensor container, per-binary pattern matching `file:stores/postgres/Dockerfile.postgres`, `file:stores/filesystem/Dockerfile.filesystem`, `file:stores/stub/Dockerfile.stub` (multi-stage `golang:1.25-alpine` builder → `gcr.io/distroless/static:nonroot`; `go build ./sensors/sensor-cron`). The sensor binaries have `main.go` at the package root — there is NO `cmd/` subdirectory — so the build target is the package itself. NOT `file:deploy/Dockerfile.go-base` — that base image only supports `cmd/`-rooted binaries.
- `sensors/sensor-http/Dockerfile.sensor-http` — same per-binary pattern.
- `sensors/sensor-object-store/Dockerfile.sensor-object-store` — same.
- `sensors/sensor-webhook/Dockerfile.sensor-webhook` — same.
- `sensors/sensor-http/state_db.go` — per-sensor state-DB module (in `package main`, alongside the existing sensor source files; NOT a `package persistence` subpackage — keeps namespace clear of `foundation/persistence`).
- `sensors/sensor-object-store/state_db.go` — same.
- `sensors/sensor-webhook/state_db.go` — same.
- `deploy/kubernetes/rimsky-chart/templates/deployment-sensor-cron.yaml` — helm deployment.
- `deploy/kubernetes/rimsky-chart/templates/service-sensor-cron.yaml` — helm service.
- (Same deployment + service pair for sensor-http, sensor-object-store, sensor-webhook.)
- `cmd/rimsky-publisher-conformance/` — new directory; contains `main.go`, `main_test.go`, `checks.go` cloned from the deleted `cmd/rimsky-sensor-conformance/` and refreshed for the publisher protocol. (Listed as Created here because the rename is implemented as add-new + delete-old in a single atomic stage; semantically it is a rename.)
- `protocols/proto/v1/publisher.proto` — new file replacing the deleted `protocols/proto/v1/sensor.proto`. Same atomic-rename pattern. After regen, `protocols/proto/v1/gen/publisher.pb.go` + `publisher_grpc.pb.go` are produced; the old `sensor.pb.go` + `sensor_grpc.pb.go` are removed.
- `runtime/publishers.go` — new file replacing the deleted `runtime/sensors.go`. Same atomic-rename pattern.
- `runtime/clientiface/publisher.go` — new file replacing the deleted `runtime/clientiface/sensor.go`.
- `runtime/remote/publisher_client.go` — new file replacing the deleted `runtime/remote/sensor_client.go`.
- `control/config/publishers.go` — new file replacing the deleted `control/config/sensors.go`.
- `foundation/persistence/publisher_subscriptions.go` — new file replacing the deleted `foundation/persistence/sensor_watches.go`.
- `foundation/persistence/postgres/publisher_subscriptions.go` — new file replacing the deleted `foundation/persistence/postgres/sensor_watches.go`.
- `foundation/persistence/sqlite/publisher_subscriptions.go` — new file replacing the deleted `foundation/persistence/sqlite/sensor_watches.go`.
- `.ok-planner/design/concepts/publisher.md` — new concept doc.
- `.ok-planner/design/concepts/publisher-subscription.md` — new concept doc (folds in the prior `sensor-watch` content). Note: NOT named `subscription.md` because that slug is already taken by the existing template-DSL `concept:subscription` (which is renamed to `concept:node-subscription` as part of this spec — see §Concept doc changes).
- `.ok-planner/design/concepts/replica.md` — new concept doc.
- `docs/concepts/publisher.md` — public-surface concept doc.
- `docs/concepts/publisher-subscription.md` — public-surface concept doc.
- `docs/concepts/replica.md` — public-surface concept doc.
- `docs/protocols/publisher.md` — public-surface protocol guide (replaces any prior `docs/protocols/sensor.md`).

### Modified

- `runtime/message_delivery.go:11-12` — file-level docstring referencing `POST /sensors/{watch_id}/observations` rewrites to cite the unified path.
- `graph/scheduler/scheduler.go:13` — file-level docstring referencing the same old route rewrites.
- `control/controlapi/app.go` — at the previous `registerSensorObservationsRoutes(rr, deps)` call site (line ~195), the call is deleted entirely; the dependencies fields `appDeps.Sensors` rename to `appDeps.Publishers`. Cascade: every consumer of `appDeps.Sensors` updates — `control/controlapi/app.go:88, 93`, `control/controlapi/instances.go:280, 483`, `runtime/validation_pipeline.go` (if it references `AppDeps.Sensors`), and any other reader (re-grep `appDeps.Sensors` / `AppDeps.Sensors` during write-plan to enumerate).
- `control/controlapi/messages.go::handleCreateMessage` — accepts `sender_kind = "publisher"` with `publisher_subscription_id` capability validation against an active row in `rimsky_publisher_subscriptions`. Accepts `Idempotency-Key` HTTP header.
- `control/controlapi/messages.go` — request struct gains `Sender`, `SenderKind`, `PublisherSubscriptionID`, `IdempotencyKey` fields.
- `control/config/stores.go` — Modified:
  - New `RemotePublishersConfig` type (paralleling `RemoteStoresConfig`).
  - New `Publishers` field on `RimskyConfig` struct.
  - New `Publishers` field on the YAML wrapper struct in `LoadRimskyConfigYAML`.
  - New `ProtocolPublisher` constant at the existing constants location (replaces `ProtocolSensor`; `ProtocolSensor` is removed).
  - The startup validation path (`case ProtocolSensor, ProtocolValidation, ProtocolDataProcessing:` at line ~437) updates: `ProtocolSensor` → `ProtocolPublisher` AND the block accepts the new protocol identity.
  - Internal-vocabulary callout: the per-protocol acceptance lists may continue accepting peers advertising the old name `"sensor"` until the rename is fully shipped; this transitional tolerance is internal-to-rimsky vocabulary. Bundled-services-layer "sensor" terminology (under `pkg:sensors/`) is unaffected — those services implement the Publisher protocol but remain named "sensor" at the bundled-services layer.
- `control/config/sensors.go` → renamed to `control/config/publishers.go`. Inside:
  - `DialSensorAndValidationRegistries` → `DialPublisherAndValidationRegistries`. Signature gains a third argument: `(ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig, publishers RemotePublishersConfig)`. The dial path now walks three maps (`claim_producers:`, `executors:`, `publishers:`) and dispatches per advertised protocol.
  - `sensorRegistryImpl` → `publisherRegistryImpl`.
  - The dual-block discovery (sensor-advertising peer found under `claim_producers:` / `executors:` blocks) is **deprecated**: from Stage 2 forward, publisher peers must live under the new top-level `publishers:` block. Document the deprecation in the YAML loader's startup-error path.
- `control/config/controlapi.go:~211` (or wherever the call lives) — call-site update to pass `cfg.Publishers` as the third argument to `DialPublisherAndValidationRegistries`.
- `licensing.yml:~47` — rename `cmd/rimsky-sensor-conformance/` entry → `cmd/rimsky-publisher-conformance/`.
- `foundation/persistence/postgres/migrations/001-baseline.sql` + `foundation/persistence/sqlite/migrations/001-baseline.sql` — baseline rewrites:
  - Rename `rimsky_sensor_watches` → `rimsky_publisher_subscriptions`.
  - Drop `on_observation` column.
  - Drop `last_observed_at` column.
  - Rename `sensor_name` → `publisher_name`.
  - Add `target_node TEXT NOT NULL` column.
  - Add `message_kind TEXT NOT NULL DEFAULT 'invalidate'` column.
  - Add the new `rimsky_message_idempotencies` table.
  - Indexes + FK constraints update accordingly.
  - Pre-v1: edit baseline directly, dev DBs wiped on every release.
- `foundation/spec/template.go::TemplateSpec.Sensors` field → `TemplateSpec.Publishers`. Template-DSL block: `sensors:` → `publishers:` everywhere it parses templates.
- `foundation/spec/graphs.go` — the `sensors:` block parsing / canonicalization sites move to `publishers:`. (Same file separately drops `OnObservationSpec` per §Dropped.)
- `graph/template/canonical/*.go` — accept the `publishers:` template-DSL key; reject the `sensors:` template-DSL key with a clear pre-v1 error: `"the template-DSL 'sensors:' block was renamed to 'publishers:' in 2026-05-17; rename your block"`. Separately, the canonicalizer rejects `payload_template` inside any per-template publisher entry; that error message points at the substitution path (see Tension 4).
- `runtime/validation_pipeline.go:99` `tpl.Sensors` iteration → `tpl.Publishers`.
- All four bundled sensors (`sensors/sensor-cron/`, `sensors/sensor-http/`, `sensors/sensor-object-store/`, `sensors/sensor-webhook/`):
  - `Subscribe` RPC handler accepts `target_node` + `message_kind` inline; persists them alongside the publisher-subscription (in the sensor's state DB, except sensor-cron which keeps them in-memory).
  - At fire time, builds the message envelope from the routing fields + raw observation bytes + publisher metadata; POSTs to `route:POST /instances/{instance_id}/messages` (not the old observation route).
  - The HTTP body sets `sender_kind: "publisher"` (matches the protocol-level role at the rimsky boundary; the bundled service IS a sensor but its role at the wire boundary is publisher). The HTTP body sets `publisher_subscription_id` (formerly `watch_id`).
  - sensor-http: adds persistence for `LastHash`. sensor-object-store: adds persistence for `WatermarkCursor`. sensor-webhook: adds persistence for `LastIdempotency` per publisher-subscription (with TTL). sensor-cron: no persistence (in-memory acceptable per design).
  - Sensor-internal naming may remain (per the design-discussion option-a): the in-memory `Watch` struct, the `postObservation` / `fireOne` helpers are sensor-internal. These are not on the wire and not in the rimsky-facing surface; they are the bundled service's local vocabulary. (Same shape as `pkg:stores/postgres::Store` implementing `ClaimProducer` — the bundled type stays "Store" even though it implements a producer protocol.)
  - Source-doc updates (file-level docstrings citing the dropped route):
    - `sensors/sensor-cron/sensor.go:7` — `POST /sensors/{watch_id}/observations` → `POST /instances/{instance_id}/messages`.
    - `sensors/sensor-http/sensor.go:8` — same swap.
    - `sensors/sensor-webhook/main.go:6` — same swap.
    - `sensors/sensor-object-store/sensor.go` has no file-level docstring citing the old route; the URL-construction site is its sole hit.
  - URL-construction site updates: all four bundled sensors' URL-construction sites (sensor-cron `sensors/sensor-cron/sensor.go:246`, sensor-http `sensors/sensor-http/sensor.go:356`, sensor-object-store `sensors/sensor-object-store/sensor.go:333`, sensor-webhook `sensors/sensor-webhook/sensor.go:289`) rewrite from `/sensors/<watch_id>/observations` to `/instances/<instance_id>/messages`.
  - `sensors/sensor-cron/sensor.go:17-19` — pre-existing docstring bug fix: "at most one extra fire per restart per watch" → "at most one MISSED fire per restart per publisher-subscription." (Tracked here so it doesn't get lost; lands in Stage 3.)
- `sensors/sensor-cron/multi_replica_test.go` — Stage-3 docstring rewrite. Drop the "When the advisory-lock implementation lands" wording (won't land per Tension 1 resolution) and rewrite to reflect that single-replica is the v1 contract per `concept:replica`. Also update any in-test references to `Watch` / `watch state` to publisher-subscription vocabulary (in-test variable names may stay sensor-internal per the "sensor-internal vocabulary stays" policy; the docstring is the load-bearing change).
- All four bundled sensors' `main.go` — register a `genv1.PublisherServer` (was `SensorServer`).
- All four bundled sensors' unit tests (`sensors/sensor-*/sensor_test.go`) — update path-matchers (`/sensors/w1/observations` → `/instances/<id>/messages` shape), struct field references (`Watch` → may stay internally; `OnObservation` references gone), and any `payload_template` fixtures.
- `test/scenarios/sensor/message_routing_test.go` (renamed from `test/scenarios/sensor/observation_routing_test.go`) — rewritten to drive the unified-route path. The capability-check rejection cases (Issue 21 from cycle-1) land here. The `test/scenarios/sensor/` directory stays as-is (these scenarios test the bundled sensor implementations, which IS sensor-specific; protocol-level tests of arbitrary publishers, if added later, would land under `test/scenarios/publisher/`).
- `test/scenarios/sensor/lifecycle_start_stop_test.go` — minor touch-up to use Subscribe/Unsubscribe verb names + the inline routing fields.
- `test/scenarios/messages/sensor_invalidate_to_cascade_test.go` — file-level docstring cite at `:8` updates from `POST /sensors/{watch_id}/observations` to the unified path; the test scenario itself already exercises the message-cascade flow.
- `test/smoke/data_platform_smoke_test.go` — the smoke flow at `:209-315` actively exercises the old observation route (constructs `pushURL := fmt.Sprintf("%s/sensors/%s/observations", rimsky.URL, watchID)` at `:290`; asserts path at `:315`). All of `:209-315` migrates to POST `/instances/<id>/messages` with `sender_kind: "publisher"` + `Idempotency-Key`.
- `deploy/build-images.sh` — builds the four sensor Dockerfiles.
- `deploy/docker-compose.yml` — adds four sensor services with `replicas: 1` (compose doesn't support replica counts directly; just runs one of each).
- `deploy/kubernetes/rimsky-chart/values.yaml` — adds defaults for the four sensors (each `replicas: 1`).
- `deploy/rimsky.yml` — adds new top-level `publishers:` block; four new peer entries land there declaring `protocols: [publisher]`. Multi-protocol peers (e.g., a peer that implements both `publisher` and `validation`) get duplicate entries across role-named blocks (one entry under `publishers:`, one entry under `validators:`, same endpoint, different protocol advertisement per block). This is the new convention: top-level role-named blocks (`claim_producers:`, `executors:`, `publishers:`, `validators:`, `data_processors:`) each carry entries for peers that play that role. `code:control/config/publishers.go::DialPublisherAndValidationRegistries` iterates a third map (the `publishers:` block) in addition to `claim_producers:` + `executors:`.
- Concept docs:
  - `.ok-planner/design/concepts/sensor.md` — **full rewrite, end-to-end** (not additive). The existing doc's protocol-methods list (`StartWatch` / `StopWatch` / `ListWatches`) is now wrong; the rewrite makes it: "Sensor is a class of Publisher implementation that observes external state. Protocol methods are inherited from Publisher (`Subscribe` / `Unsubscribe` / `ListSubscriptions`). Examples in this repo: sensor-cron, sensor-http, sensor-object-store, sensor-webhook."
  - `.ok-planner/design/concepts/sensor-watch.md` — deleted; content folded into `.ok-planner/design/concepts/publisher-subscription.md` (see Created).
  - `.ok-planner/design/concepts/subscription.md` → renamed to `.ok-planner/design/concepts/node-subscription.md`. Sweep all cross-references in other concept docs (`wait-set.md`, `node.md`, related tension files) to read `concept:node-subscription`. Update code annotations from `@concept: subscription` → `@concept: node-subscription`. The publisher-side concept lives at `.ok-planner/design/concepts/publisher-subscription.md` (see Created); the two concepts are orthogonal and must stay distinct in cross-references.
  - `docs/concepts/subscription.md` → renamed to `docs/concepts/node-subscription.md` (public-surface counterpart of the internal rename).
  - `.ok-planner/design/concepts.md` (the internal concept-catalog TOC) — update the TOC: drop the old `subscription` slug; add `node-subscription`, `publisher-subscription`, `publisher`, `replica`.
  - Public concept TOC (`docs/concepts/` index, if one exists) — same update.
  - `.ok-planner/design/concepts/message.md` — adds idempotency-key subsection; rewrites "Three emit sites" line to cite `POST /instances/{id}/messages` with `sender_kind: "publisher"`; **updates the `sender_kind` enum at message.md:22-23 from `(operator | sensor | instance)` → `(operator | publisher | instance)`**; annotation-sites list drops the `code:control/controlapi/sensors.go::handleSensorObservation` entry.
  - `.ok-planner/design/concepts/invalidate.md:42` — rewrite the line that cites `POST /sensors/{watch_id}/observations` to cite the unified `POST /instances/{id}/messages` with `sender_kind: "publisher"`.
  - `.ok-planner/design/concepts/named-event.md:51` — replace the "sensor observation (...)" phrasing with "publisher-origin message" + the unified-route citation.
  - `.ok-planner/design/concepts/backfill.md:59` — replace "sensor observations" with "publisher-origin messages" and update the cite.
  - `.ok-planner/design/concepts/frame.md:18` — replace "sensor observations" (cited as a frame-creation site) with "publisher-origin messages" and update.
- `docs/concepts/sensor.md` (if it exists) — refreshed to match the internal sensor concept doc.
- `feature-index.md` — two row edits:
  - Row at `feature-index.md:78` — rename `rimsky-sensor-conformance` → `rimsky-publisher-conformance`.
  - Row at `feature-index.md:81` — **delete the row entirely** (stale "Reference sensor binary (cron firing)" pointing at a non-existent binary; the actual sensor lives under `pkg:sensors/sensor-cron/`, not `cmd/rimsky-sensor-cron/`).
  - Also sweep `feature-index.md` for any row citing `concept:subscription` and update to `concept:node-subscription`; for any row citing `rimsky_sensor_watches`, update to `rimsky_publisher_subscriptions`.
- `CLAUDE.md` — surgical edits across multiple sites:
  - Every "Sensor protocol" → "Publisher protocol."
  - Every "sensor watch" / "watch" (when referring to the rimsky-side binding) → "publisher-subscription."
  - `rimsky_sensor_watches` → `rimsky_publisher_subscriptions` (~5 hits in schema + gotcha sections).
  - **`rimsky_messages.sender_kind` enum at CLAUDE.md:154 — update from `(operator | sensor | instance)` → `(operator | publisher | instance)` to match the post-spec wire vocabulary.**
  - In the cfg description: introduces top-level `publishers:` block (alongside `claim_producers:` + `executors:`).
  - Drops the `POST /sensors/{watch_id}/observations` route reference; replaces with the unified-route operator-facing note.
  - The "Cron firing is owned by sensors/sensor-cron/" gotcha — content unchanged, vocabulary aligned (`publisher peer` instead of `sensor peer` where appropriate; "bundled sensor" stays for the service name).
  - New gotcha block describing the universal `Idempotency-Key` header on `POST /instances/{id}/messages`.
  - New gotcha block describing the dropped `POST /sensors/{watch_id}/observations` route — flag it for operators who might have external sensors pointing at the old URL.
  - Schema row for `rimsky_publisher_subscriptions`: column list reads roughly `(publisher_name, publisher_subscription_id)` keying, plus `instance_id`, `kind`, `resolved_config JSONB`, `target_node`, `message_kind`, `started_at`, `state (active | failed | stopped)`. No `on_observation`, no `last_observed_at`.
  - Any `concept:subscription` citation in CLAUDE.md that refers to the template-DSL receiver-side block updates to `concept:node-subscription`; any new citation introduced for the publisher binding uses `concept:publisher-subscription`.
- `CHANGELOG.md` — under `## Unreleased`.

### Dropped

- `route:POST /sensors/{watch_id}/observations` route + handler entirely.
- `code:control/controlapi/sensors.go` — entire file deleted (~293 lines; all observation handling).
- `code:control/controlapi/sensors_test.go` — entire file deleted.
- `code:control/controlapi/sensors.go::substituteObservationTemplate` — gone with the file; no replacement (substitution moves to dispatch-time attribute layer).
- `code:control/controlapi/app.go` — the `registerSensorObservationsRoutes(rr, deps)` call at line ~195 is removed.
- `col:rimsky_sensor_watches.on_observation` — column dropped (and the table itself renamed to `rimsky_publisher_subscriptions`).
- `col:rimsky_sensor_watches.last_observed_at` — column dropped.
- `code:foundation/spec/graphs.go::OnObservationSpec` — entire Go type deleted (not renamed; the routing fields move inline into the proto, no Go-side substruct needed).
- `code:graph/node/template.go:58::OnObservationSpec` — the type alias deleted alongside the underlying type.
- `protocols/proto/v1/sensor.proto` — deleted; replaced by `protocols/proto/v1/publisher.proto`.
- `protocols/proto/v1/gen/sensor.pb.go` + `protocols/proto/v1/gen/sensor_grpc.pb.go` — regenerated as `publisher.pb.go` + `publisher_grpc.pb.go`.
- `runtime/sensors.go` → renamed to `runtime/publishers.go` (delete + create).
- `runtime/clientiface/sensor.go` → renamed to `runtime/clientiface/publisher.go`.
- `runtime/remote/sensor_client.go` → renamed to `runtime/remote/publisher_client.go`.
- `control/config/sensors.go` → renamed to `control/config/publishers.go`.
- `foundation/persistence/sensor_watches.go` → renamed to `foundation/persistence/publisher_subscriptions.go`.
- `foundation/persistence/postgres/sensor_watches.go` + `foundation/persistence/sqlite/sensor_watches.go` → renamed to `foundation/persistence/{postgres,sqlite}/publisher_subscriptions.go`.
- `cmd/rimsky-sensor-conformance/` → renamed to `cmd/rimsky-publisher-conformance/`.
- `cmd/rimsky-sensor-cron/` — orphan empty directory; **delete entirely**. The actual sensor binary lives at `pkg:sensors/sensor-cron/` (with `main.go` at the package root), not under `cmd/`. This directory is dead code carrying nothing.
- `.ok-planner/design/concepts/sensor-watch.md` — deleted; content folded into `concept:publisher-subscription`.

## Architecture details

### Publisher protocol verbs + types (proto level)

The proto file `protocols/proto/v1/publisher.proto` defines:

```protobuf
service Publisher {
  rpc Capabilities      (CapabilitiesRequest)      returns (PublisherCapabilities);
  rpc Subscribe         (SubscribeRequest)         returns (SubscribeResponse);
  rpc Unsubscribe       (UnsubscribeRequest)       returns (UnsubscribeResponse);
  rpc ListSubscriptions (ListSubscriptionsRequest) returns (ListSubscriptionsResponse);
}

message PublisherCapabilities {
  repeated PublisherKindCapability kinds = 1;
  // ...
}

message PublisherKindCapability {
  string kind = 1;
  // ...
}

message SubscribeRequest {
  string publisher_subscription_id = 1;
  string instance_id               = 2;
  string kind                      = 3;
  bytes  resolved_config           = 4;
  string target_node               = 5;   // inline; no on_change substruct
  string message_kind              = 6;   // default 'invalidate' if empty
}

message PublisherSubscriptionDescriptor {
  string publisher_subscription_id = 1;
  string instance_id               = 2;
  string kind                      = 3;
  bytes  resolved_config           = 4;
  string target_node               = 5;
  string message_kind              = 6;
  string state                     = 7;
}
```

Field rename `watch_id` → `publisher_subscription_id` is global: proto, URL paths, Go code, tests, log keys. The routing fields (`target_node`, `message_kind`) are inline on `SubscribeRequest` — no `on_change` / `OnObservation` substruct. After proto changes, `cmd:make proto-gen` regenerates `protocols/proto/v1/gen/*.pb.go`.

### Go-side type names (post-rename)

- `runtime/publishers.go`: `PublisherRegistry`, `PublisherClient`, `StartPublisherSubscriptionsForInstance`, `StopPublisherSubscriptionsForInstance`, `ResyncPublisherSubscriptions`. Log key `publisher.subscribe.marshal_failed` (replaces today's `sensor.start.on_observation_marshal_failed`; the marshal target also goes away since routing fields are inline scalars now, but a remaining marshal site exists for `resolved_config` — the log-key rename absorbs both cases).
- `runtime/clientiface/publisher.go`: `SubscribeRequest` Go struct mirrors the proto type 1:1 (inline routing fields).
- `runtime/remote/publisher_client.go`: gRPC client wire mapping.
- `foundation/persistence/publisher_subscriptions.go`: `PublisherSubscriptionTable` interface; `PublisherSubscriptionRow` struct with fields `{PublisherSubscriptionID, InstanceID, PublisherName, Kind, ResolvedConfig, TargetNode, MessageKind, StartedAt, State}`.
- `foundation/persistence/{postgres,sqlite}/publisher_subscriptions.go`: driver impls.
- `control/config/publishers.go`: `DialPublisherAndValidationRegistries`, `publisherRegistryImpl`. The `appDeps.Sensors` field renames to `appDeps.Publishers`.
- `control/controlapi/messages.go`: request struct gains `PublisherSubscriptionID` (not `WatchID`).
- `cmd/rimsky-publisher-conformance/{main.go, main_test.go, checks.go}`: conformance binary.

### Schema (post-baseline rewrite)

Both `001-baseline.sql` files (postgres + sqlite) carry:

```sql
CREATE TABLE rimsky_publisher_subscriptions (
    id              UUID NOT NULL,                -- publisher_subscription_id
    instance_id     UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    publisher_name  TEXT NOT NULL,
    kind            TEXT NOT NULL,
    resolved_config JSONB NOT NULL,
    target_node     TEXT NOT NULL,                -- nullability decision in Open Items
    message_kind    TEXT NOT NULL DEFAULT 'invalidate',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    state           TEXT NOT NULL CHECK (state IN ('active','failed','stopped')),
    PRIMARY KEY (publisher_name, id)
);

CREATE TABLE rimsky_message_idempotencies (
    instance_id      UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    sender           TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    message_id       UUID NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, sender, idempotency_key)
);
CREATE INDEX idx_message_idempotencies_created_at
    ON rimsky_message_idempotencies(created_at);
```

SQLite mirror uses `TEXT NOT NULL DEFAULT (datetime('now'))` for timestamps. No `on_observation`, no `last_observed_at`. Pre-v1 baseline rewrite; no append-only ADD/DROP migrations.

### Message idempotency (universal)

`route:POST /instances/{id}/messages` accepts an `Idempotency-Key` header (string, ≤256 chars). When present:

1. Rimsky computes the dedup key as `(instance_id, sender, idempotency_key)`.
2. INSERT-or-lookup into `table:rimsky_message_idempotencies`:
   - On INSERT: row created; proceed to insert the message envelope; record `message_id` against the idempotency row. Return `201 Created` with body `{message_id: <new>}`.
   - On conflict (key already exists): return the existing `message_id` with `200 OK` and body `{message_id: <existing>}` — identical body shape to the 201 case; status code is the only signal of replay vs fresh.
3. TTL: idempotency rows expire after 24 hours (configurable per `cfg:retention.message_idempotencies_trailing` — same pattern as `cfg:retention.claim_handles_trailing`).
4. Sweep: new `runtime/sweep_message_idempotencies.go` runs under the scheduler-tick advisory lock; analogous to `code:runtime/sweep_claim_handle_retention.go`.

The idempotency feature is universal — operators sending invalidates with retry logic, publishers firing messages, lifecycle handlers (if they ever gain retry) all use the same `Idempotency-Key` header. Bundled publishers generate keys per fire (cron: `{publisher_subscription_id}+{fire_window_iso}`; http: `{publisher_subscription_id}+{body_sha256}`; object-store: `{publisher_subscription_id}+{object_etag}`; webhook: `{publisher_subscription_id}+{idempotency_header_value}`).

### Publisher Subscribe payload + message-envelope shape

`proto:publisher.proto::SubscribeRequest` carries `target_node` + `message_kind` as inline scalars (no substruct). The publisher:

1. Stores `target_node` + `message_kind` alongside the publisher-subscription's other state (in memory or in its DB). They are set once at Subscribe and never updated mid-publisher-subscription. Template publisher-block changes flow through a new template hash → new instance → new `publisher_subscription_id`; the old publisher-subscription is `Unsubscribe`-d and a new one is `Subscribe`-d with the new routing. There is no mid-publisher-subscription reconfiguration verb.
2. At fire time, builds:
   ```json
   {
     "kind": "<message_kind, default 'invalidate'>",
     "target": "<target_node>",
     "payload": <raw observation as JSON>,
     "sender": "<publisher_name>",
     "sender_kind": "publisher",
     "publisher_subscription_id": "<publisher_subscription_id>",
     "idempotency_key": "<publisher-computed key>"
   }
   ```
3. POSTs to `route:POST /instances/{instance_id}/messages` with `Idempotency-Key: <same>` HTTP header.

`publisher_subscription_id` is consumed at the capability-check boundary (see next section) and dropped; the persisted `rimsky_messages` row carries `sender = publisher_subscription.publisher_name` and `sender_kind = 'publisher'` only — not `publisher_subscription_id`. Operator observability ("did this publisher-subscription fire?") is via `route:GET /instances/{id}/messages?sender=<publisher_name>` filtering.

**Note on `sender_kind` choice.** The value `"publisher"` is chosen (not `"sensor"`) because the role at the rimsky boundary is the protocol-level role: a publisher is publishing into rimsky. The bundled implementation might be a sensor, but the wire-level role is publisher. This matches the analogous `ClaimProducer` boundary, where producers' bundled implementations are "stores" but their wire role is "claim_producer." Operators reading messages filter by the wire role; the bundled service domain (sensor vs. some future non-sensor publisher) is not a wire concern.

### `sender_kind: "publisher"` capability check

When `route:POST /instances/{id}/messages` receives a request with `sender_kind: "publisher"`:

1. Body MUST include `publisher_subscription_id`.
2. Rimsky resolves the publisher-subscription row: `SELECT * FROM rimsky_publisher_subscriptions WHERE id = $publisher_subscription_id AND instance_id = $instance_id AND state = 'active'`.
3. If no row: `403 Forbidden` (or `404` — pick at write-plan; 403 is more honest about "I see the request but you're not authorized").
4. Rimsky overwrites `body.sender = publisher_subscription.publisher_name` (don't trust sender from request; derive from authoritative row). This means publishers can't spoof other publishers' names.
5. Proceed with the message insert.

This gives capability-style auth: a publisher that knows a valid `publisher_subscription_id` can post for that instance. The `publisher_subscription_id` was minted by rimsky at instance create and only shared with the publisher at Subscribe time. Publisher implementations are trusted within the rimsky deployment perimeter (same trust model as executors).

**V1 trust model**: network perimeter; the `publisher_subscription_id` capability check stops trivial cross-instance spoofing, but a compromised peer inside the perimeter could in principle read `rimsky_publisher_subscriptions` and forge a `publisher_subscription_id`. TLS / mTLS is deferred (post-v1 Plan C, same as executors per `code:runtime/executor/client.go`). Operators running rimsky outside a trusted network perimeter should not deploy publishers until the TLS work lands.

**Stage 1 timing note (cycle-4 resolution).** The publisher capability check is NOT added in Stage 1. Stage 1's `handleCreateMessage` continues to treat omitted `sender_kind` as `"operator"`; the `"publisher"` value, `publisher_subscription_id` body field, and capability check are introduced atomically in Stage 2 alongside the protocol rename. Stage 1 is idempotency-infrastructure only.

### State persistence in bundled sensors

Each bundled sensor that needs state persistence opens its own `pgxpool.Pool` (or SQLite db handle) configured via env var (`env:RIMSKY_SENSOR_HTTP_STATE_DSN`, `env:RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN`, `env:RIMSKY_SENSOR_WEBHOOK_STATE_DSN`, `env:RIMSKY_SENSOR_CRON_STATE_DSN`). DSN-from-env-var only — no `cfg:rimsky.yml` keys for sensor state DBs; the helm chart wires the env var via Deployment manifests.

On startup:

1. If no DSN: in-memory mode (current behavior; restart loses state).
2. If DSN: open the connection; run schema migration (CREATE TABLE IF NOT EXISTS — small enough to do without a migration runner); load any persisted subscriptions into the in-memory map.

Schema shape (illustrative; sensor-http):

```sql
CREATE TABLE IF NOT EXISTS sensor_http_state (
    publisher_subscription_id TEXT PRIMARY KEY,
    instance_id               TEXT NOT NULL,
    url                       TEXT NOT NULL,
    poll_interval             TEXT NOT NULL,
    match_status              TEXT NOT NULL,
    match_json_key            TEXT,
    match_json_val            TEXT,
    target_node               TEXT NOT NULL,
    message_kind              TEXT NOT NULL,
    last_poll_at              TIMESTAMPTZ,
    last_hash                 TEXT,
    started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

State writes happen in the tick loop after a successful message post: UPDATE the row with new `last_poll_at` + `last_hash`. State writes for Subscribe / Unsubscribe are insert/delete.

sensor-object-store and sensor-webhook follow the same pattern with their respective state columns.

sensor-cron's state is reconstructible (`sched.Next(now)`), so persistence is optional and gated behind the DSN env var; in dev / single-replica without DSN, it stays in-memory and the cost is documented as "at most one MISSED fire per restart per publisher-subscription."

**Depguard + third-party imports**: each sensor that gains a state DB introduces new third-party imports — `jackc/pgx/v5` for postgres backends, `modernc.org/sqlite` for dev. The `code:.golangci.yml` depguard `pgx-isolation` rule's allow-list needs extending to include `sensors/sensor-http/`, `sensors/sensor-object-store/`, `sensors/sensor-webhook/`. Each sensor's state-DB module adds ~100 lines (schema bootstrap + state CRUD). Stage 3 quiescence verification gate MUST include `make license-lint` + `make lint` to catch depguard regressions.

**Package layout**: each sensor's state-DB code lives in `package main` alongside the existing sensor source files (NOT in a `package persistence` subpackage — avoids namespace collision with `foundation/persistence` and keeps the cold-read of each sensor under a single namespace). Typical file name: `sensors/sensor-<kind>/state_db.go`.

### Publisher-side message-emit retry

Publishers POSTing to `route:POST /instances/{id}/messages` should retry on 5xx + connection errors (~3 attempts, exp backoff 200ms→1.6s). On final failure: warn-log + abandon (don't advance the publisher's state; the next tick retries the same fire window — same shape as today's sensor-cron `fireOne` failure path). Each publisher implements this internally.

On 403/404 (publisher-subscription not active — typically the result of an `Unsubscribe` race or an instance termination that hasn't yet propagated): the publisher logs at WARN (`publisher.message.rejected` with fields `{publisher_subscription_id, status_code, instance_id}`) and drops the observation. The next publisher-tick will discover the publisher-subscription is no longer in its in-memory map (because `ResyncPublisherSubscriptions` or `Unsubscribe` removed it) and stop polling.

### Rimsky-side Subscribe retry

`code:runtime/publishers.go::StartPublisherSubscriptionsForInstance` wraps the `Subscribe` RPC call with retry-with-backoff (~3 attempts, exp 200ms→1.6s, jittered). Rationale: under a rolling restart of the publisher pods (e.g., the deploy that lands the bundled-sensor images in Stage 3; or any operator-driven publisher redeploy), the first Subscribe RPC after rimsky receives `create-instance` can hit a publisher pod that is mid-transition. The retry gives the new pod a moment to come up before rimsky concludes the publisher-subscription is unstartable. Failure mode after exhausting retries is unchanged: warn-log + flip the publisher-subscription row to `state='failed'` (same shape as today's single-attempt failure path); the retry only reduces the rate of false-positive failures during transient publisher unavailability.

### Deploy artifacts

Each bundled sensor gets a Dockerfile in the per-binary pattern matching `file:stores/postgres/Dockerfile.postgres`, `file:stores/filesystem/Dockerfile.filesystem`, `file:stores/stub/Dockerfile.stub` — multi-stage `golang:1.25-alpine` builder, distroless-static runtime, non-root user, the binary baked in via `go build ./sensors/sensor-<kind>` (the sensor binaries have `main.go` at the package root; there is no `cmd/` subdirectory). This is NOT the `file:deploy/Dockerfile.go-base` pattern (which only handles `cmd/`-rooted binaries). The build script `file:deploy/build-images.sh` extends to include the four new images:

```bash
docker build -f sensors/sensor-cron/Dockerfile.sensor-cron -t rimsky/sensor-cron:$VERSION -t rimsky/sensor-cron:latest .
docker build -f sensors/sensor-http/Dockerfile.sensor-http -t rimsky/sensor-http:$VERSION -t rimsky/sensor-http:latest .
docker build -f sensors/sensor-object-store/Dockerfile.sensor-object-store -t rimsky/sensor-object-store:$VERSION -t rimsky/sensor-object-store:latest .
docker build -f sensors/sensor-webhook/Dockerfile.sensor-webhook -t rimsky/sensor-webhook:$VERSION -t rimsky/sensor-webhook:latest .
```

`file:deploy/docker-compose.yml` adds four sensor services connecting to the rimsky control-api + (where applicable) a state postgres database. The default docker-compose stack runs one of each sensor.

Helm chart gains four deployments + four services. Default `replicas: 1` per the single-replica architectural decision. Each declares `RIMSKY_ENDPOINT` and (where applicable) `RIMSKY_SENSOR_<KIND>_STATE_DSN`. The chart's `values.yaml` enumerates each sensor's enable flag (`enabled: true/false`), replicas count, and state-DB connection settings.

The `file:deploy/rimsky.yml` reference config gains a top-level `publishers:` block with four entries declaring `protocols: [publisher]`. Multi-protocol peers get duplicate entries across role-named blocks; e.g., a peer implementing both `publisher` and `validation` has one entry under `publishers:` (with `protocols: [publisher]`) and one entry under `validators:` (with `protocols: [validation]`), same endpoint. The dial path is `code:control/config/publishers.go::DialPublisherAndValidationRegistries`, which now walks three maps (claim_producers, executors, publishers) and dispatches per advertised protocol.

## Concept doc changes

### New: `concept:publisher`

Internal: `.ok-planner/design/concepts/publisher.md`. Public: `docs/concepts/publisher.md`.

Definition: A publisher is a peer service that pushes messages into rimsky. Publishers implement the `proto:publisher.proto::Publisher` protocol (4 verbs: `Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`). Publishers are out-of-process, gRPC-addressed, peer-services in the same trust perimeter as executors and claim-producers.

A sensor is one kind of publisher — specifically, a publisher whose source of messages is observation of external state (cron clock, HTTP endpoint, object-store prefix, webhook port). Other kinds of publishers may exist in the future (e.g., a publisher whose source is a pub/sub topic); the protocol is general.

Cross-references: `concept:sensor`, `concept:publisher-subscription`, `concept:message`.

### New: `concept:publisher-subscription`

Internal: `.ok-planner/design/concepts/publisher-subscription.md`. Public: `docs/concepts/publisher-subscription.md`.

Replaces the deleted `concept:sensor-watch`. Describes the post-spec shape: publisher-subscription row is registration metadata only (`id, instance_id, publisher_name, kind, resolved_config, target_node, message_kind, started_at, state`). `last_observed_at` is not a publisher-subscription property — observability comes from querying `route:GET /instances/{id}/messages?sender=<publisher_name>`. The routing fields (`target_node`, `message_kind`) are persisted on the row and re-presented at `ResyncPublisherSubscriptions` after publisher restart.

**Naming note (cycle-4 resolution).** This concept is named `publisher-subscription` (not `subscription`) because `concept:subscription` was already the slug for the template-DSL receiver-side concept (`subscribes:` block on a node). That existing concept renames to `concept:node-subscription` in this spec; see "Rename: `concept:subscription` → `concept:node-subscription`" below.

### Rename: `concept:subscription` → `concept:node-subscription`

Internal: rename `.ok-planner/design/concepts/subscription.md` → `.ok-planner/design/concepts/node-subscription.md`. Public: rename `docs/concepts/subscription.md` → `docs/concepts/node-subscription.md`.

Content-level change is purely the slug rename + every cross-reference. Body text gains a leading clarifier paragraph: "This concept describes the **receiver-side** template-DSL subscription declared in a node's `subscribes:` block — a node's wait-set on a sibling's terminal-changed signal. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal."

Cross-reference sweep:
- Every other concept doc that cites `concept:subscription` updates to `concept:node-subscription`. Enumerated set (from `rg 'concept:subscription' .ok-planner/design/concepts/`): `invalidate.md:41`, `node.md:42`, `last-outcome.md:49`, `message.md:30`, `lifecycle-handler.md:66`, `cascade.md:48`, `wait-set.md:32`, `_retired/on-event-handler.md:11`. Plus any tension files surfaced by re-grep at execute time.
- Every in-code annotation `@concept: subscription` → `@concept: node-subscription` (re-grep `@concept: subscription` across `foundation/`, `graph/`, `runtime/`, `control/`).
- `feature-index.md` rows that cite `concept:subscription` update to `concept:node-subscription`.
- `.ok-planner/design/concepts.md` TOC: drop the old `subscription` entry; add `node-subscription`, `publisher-subscription`, `publisher`, `replica`.
- Public TOC (`docs/concepts/` index) — same TOC update.

### New: `concept:replica`

Internal: `.ok-planner/design/concepts/replica.md`. Public: `docs/concepts/replica.md`.

Definition: A replica is one running pod/process of a binary, behind a load-balancing layer in the deployment tier. Multiple replicas of the same binary are interchangeable from rimsky's runtime perspective; rimsky models named peers (one per `file:deploy/rimsky.yml` `claim_producers[]` / `executors[]` / `publishers[]` entry), not pod counts. Replica count is set via the deployment platform (k8s Deployment `replicas:`, docker compose `deploy.replicas`); rimsky's runtime is unaware.

Boundaries:
- Replicas are NOT individually addressable from rimsky. The gRPC client picks one replica per connection.
- Replicas DO NOT share rimsky-runtime state; each replica's in-memory state is independent unless the binary itself implements shared state.
- Replica-level HA is the deployment platform's + the binary's joint concern; rimsky does not detect, heartbeat, or failover replicas.

Cross-references: `concept:executor`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:claim-producer` all gain a "replica behavior" subsection pointing to this concept.

### Rewrite: `concept:sensor` (end-to-end, not additive)

The existing concept doc lists the protocol surface as `StartWatch` / `StopWatch` / `ListWatches` — that surface no longer exists after Stage 2. The doc is rewritten end-to-end (not patched additively) to read:

"Sensor is a class of Publisher implementation that observes external state. Protocol methods are inherited from Publisher (`Subscribe` / `Unsubscribe` / `ListSubscriptions`). Examples in this repo: `sensor-cron`, `sensor-http`, `sensor-object-store`, `sensor-webhook`. State-persistence and multi-replica HA are the sensor implementation's concern; rimsky models one named publisher peer per `protocols: [publisher]` advertisement on an entry in the top-level `publishers:` block of `file:deploy/rimsky.yml`, and does not model pod counts. The bundled reference impls declare single-replica deployment models with state-persistence (where required) to survive restart."

Old content dropped: the protocol-surface table (referencing `StartWatch` / `StopWatch` / `ListWatches`), any aspirational language about per-publisher-subscription advisory locks or multi-replica coordination from earlier drafts.

### Refresh: `concept:message`

- Adds: idempotency-key subsection describing the `Idempotency-Key` header pattern, the `(instance_id, sender, idempotency_key)` dedup tuple, the 24h TTL, and the `rimsky_message_idempotencies` table.
- Rewrites the "Three emit sites" line: drops the `POST /sensors/{watch_id}/observations` citation; replaces "sensor observations" wording with "publisher-origin messages (`POST /instances/{id}/messages` with `sender_kind: 'publisher'`)".
- Annotation-sites list: drops the `code:control/controlapi/sensors.go::handleSensorObservation` entry (handler is gone).

### Refresh: `concept:invalidate`

Line ~42 cites the dropped `POST /sensors/{watch_id}/observations` as a construction site for invalidate-kind messages. Rewrite to cite `POST /instances/{id}/messages` with `sender_kind: "publisher"`.

### Refresh: `concept:named-event`

Line ~51 references "sensor observation (...)" as a contrast point with frame-synchronous events. Rewrite to "publisher-origin message" with the unified-route citation.

### Refresh: `concept:backfill`

Line ~59 references "sensor observations" alongside operator-API invalidates in the dispatch-machinery uniformity argument. Rewrite to "publisher-origin messages" and update the cite.

### Refresh: `concept:frame`

Line ~18 lists "sensor observations" as a frame-creation site. Rewrite to "publisher-origin messages" with the unified-route citation.

## Migration plan

Three stages. Each ends with `cmd:make build-all && make test-all && make lint && make license-lint` clean as a quiescence check before the next.

### Stage 1: Idempotency infrastructure (additive)

1. Edit baseline migrations (postgres + sqlite `001-baseline.sql`) to add `table:rimsky_message_idempotencies`. Pre-v1 schema: rewrite baseline rather than appending a migration.
2. `code:foundation/persistence/message_idempotencies.go` interface + driver impls.
3. `code:runtime/sweep_message_idempotencies.go` retention sweep + scheduler-tick wiring (same pattern as `code:runtime/sweep_claim_handle_retention.go`).
4. `code:control/controlapi/messages.go::handleCreateMessage` accepts `Idempotency-Key` header; dedupes against the new table. Duplicate response: `200 OK` with body `{message_id: <existing>}` (status code is the only signal of replay vs fresh).
5. Tests: new fixture exercising duplicate-POST returns same message_id.

Stage 1 quiescence: idempotency works for operators. Touches no publisher code. Universal benefit; least-risk stage; lands first.

**Stage 1 timing note (cycle-4 resolution).** The publisher capability check is NOT added in Stage 1; that lands in Stage 2 atomically with the protocol rename. Stage 1's `handleCreateMessage` still treats omitted `sender_kind` as `"operator"` and rejects any explicit `sender_kind: "publisher"` value (the path doesn't exist yet). The `Idempotency-Key` header IS accepted in Stage 1 for `operator`-kind requests.

### Stage 2: Publisher unification (atomic architectural change)

**This stage is intentionally a big atomic change rather than incremental.** It is the architectural unification — the protocol rename, the observation-route drop, the schema-baseline rewrite, the bundled-sensor cutover, and the proto regeneration all target the same surfaces. Incrementality was previously serving a migration-discipline need that the migration-flatten removed; without that need, splitting the change creates more partial-state churn than landing it once. Pre-v1 break-freely; dev DBs get wiped.

**Substep ordering matters within Stage 2.** Proto rename + `make proto-gen` runs FIRST, before any Go-side source rename that would touch `genv1.RegisterPublisherServer` references. If the Go-source rename precedes the proto regen, the build wedges mid-stage on missing generated symbols. Substeps are listed in execution order below.

1. **Proto rename (runs FIRST so generated bindings exist before Go-source rename touches them):**
   - Delete `protocols/proto/v1/sensor.proto`; create `protocols/proto/v1/publisher.proto`.
   - `service Sensor` → `service Publisher`. `SensorCapabilities` → `PublisherCapabilities`. `SensorKindCapability` → `PublisherKindCapability`. Verbs: `StartWatch` → `Subscribe`, `StopWatch` → `Unsubscribe`, `ListWatches` → `ListSubscriptions`. Request/response types and `WatchDescriptor` → `PublisherSubscriptionDescriptor` rename per the standard pattern. `watch_id` → `publisher_subscription_id` everywhere.
   - `SubscribeRequest` carries `target_node` + `message_kind` as inline scalars (no substruct).
   - `cmd:make proto-gen` regenerates `protocols/proto/v1/gen/publisher.pb.go` + `publisher_grpc.pb.go`; old `sensor.pb.go` files are deleted.

2. **Schema baseline rewrite (postgres + sqlite `001-baseline.sql`):**
   - Rename `rimsky_sensor_watches` → `rimsky_publisher_subscriptions`.
   - Drop `on_observation` column.
   - Drop `last_observed_at` column.
   - Rename `sensor_name` → `publisher_name`.
   - Add `target_node TEXT NOT NULL`.
   - Add `message_kind TEXT NOT NULL DEFAULT 'invalidate'`.
   - Indexes + FK constraints update.

3. **Go-side renames:**
   - `runtime/sensors.go` → `runtime/publishers.go`. `runtime/clientiface/sensor.go` → `.../publisher.go`. `runtime/remote/sensor_client.go` → `.../publisher_client.go`. `control/config/sensors.go` → `.../publishers.go`. `foundation/persistence/sensor_watches.go` → `.../publisher_subscriptions.go`. `foundation/persistence/{postgres,sqlite}/sensor_watches.go` → `.../publisher_subscriptions.go`. `cmd/rimsky-sensor-conformance/` → `cmd/rimsky-publisher-conformance/`.
   - Type renames: `SensorRegistry` → `PublisherRegistry`, `SensorClient` → `PublisherClient`, `SensorWatchRow` → `PublisherSubscriptionRow`, `SensorWatchTable` → `PublisherSubscriptionTable`, `sensorRegistryImpl` → `publisherRegistryImpl`. Function renames: `StartWatchesForInstance` → `StartPublisherSubscriptionsForInstance`, `StopWatchesForInstance` → `StopPublisherSubscriptionsForInstance`, `ResyncSensorWatches` → `ResyncPublisherSubscriptions`. `DialSensorAndValidationRegistries` → `DialPublisherAndValidationRegistries`. Field: `appDeps.Sensors` → `appDeps.Publishers`. Constant: `ProtocolSensor` → `ProtocolPublisher` (at `control/config/stores.go`).
   - `tpl.Sensors` iteration in `runtime/validation_pipeline.go:99` → `tpl.Publishers`. Template canonicalizer accepts `publishers:` template-DSL key; rejects `sensors:` template-DSL key with the documented pre-v1 error message.
   - The Subscribe RPC call in `StartPublisherSubscriptionsForInstance` is wrapped in retry-with-backoff (~3 attempts, exp 200ms→1.6s, jittered) per §Architecture details.
   - Log key `sensor.start.on_observation_marshal_failed` → `publisher.subscribe.marshal_failed`.

4. **`OnObservationSpec` Go type deletion:**
   - `code:foundation/spec/graphs.go::OnObservationSpec` — entire type definition deleted.
   - `code:graph/node/template.go:58` `type OnObservationSpec = spec.OnObservationSpec` alias — deleted.
   - `graph/template/canonical/*.go` — template canonicalizer rejects `payload_template` with the documented error message.

5. **Operator-facing route + handler removal:**
   - Delete `code:control/controlapi/sensors.go` entirely.
   - Delete `code:control/controlapi/sensors_test.go` entirely.
   - Remove `registerSensorObservationsRoutes(rr, deps)` call at `code:control/controlapi/app.go:~195`.

6. **Universal messages endpoint accepts `sender_kind: "publisher"`:**
   - `code:control/controlapi/messages.go::handleCreateMessage` accepts `sender_kind = "publisher"` with `publisher_subscription_id` capability validation against an active row in `rimsky_publisher_subscriptions`. Request struct gains `Sender`, `SenderKind`, `PublisherSubscriptionID` fields (the `IdempotencyKey` field was added in Stage 1).
   - Capability-check rejection cases: (1) unknown `publisher_subscription_id`; (2) `publisher_subscription_id` with mismatched `instance_id` (cross-instance spoof attempt); (3) `publisher_subscription_id` for stopped/failed publisher-subscriptions; (4) request with `sender_kind="publisher"` but missing `publisher_subscription_id`.

7. **Bundled sensor cutover:**
   - All four sensors' `main.go` register a `genv1.PublisherServer`.
   - Each sensor's Subscribe handler accepts `target_node` + `message_kind` inline; persists them alongside the publisher-subscription.
   - URL-construction sites switch from `/sensors/<watch_id>/observations` to `/instances/<instance_id>/messages`. HTTP body uses `sender_kind: "publisher"` + `publisher_subscription_id` + `Idempotency-Key`.
   - File-level docstrings updated: `sensors/sensor-cron/sensor.go:7`, `sensors/sensor-http/sensor.go:8`, `sensors/sensor-webhook/main.go:6`.
   - Sensor-internal vocabulary (`Watch` struct, `postObservation`, `fireOne`) stays — these are sensor-local, not on the wire.

8. **Tests:**
   - `test/scenarios/sensor/observation_routing_test.go` → renamed `test/scenarios/sensor/message_routing_test.go`. Rewritten to drive the unified-route path. Capability-check rejection cases land here.
   - `test/scenarios/sensor/lifecycle_start_stop_test.go` — minor touch-up to use Subscribe/Unsubscribe verb names + the inline routing fields.
   - `test/scenarios/messages/sensor_invalidate_to_cascade_test.go:8` — docstring cite update.
   - `test/smoke/data_platform_smoke_test.go:209-315` — migrate the smoke flow to POST `/instances/<id>/messages` with `sender_kind: "publisher"`.
   - Per-sensor unit tests update path-matchers + struct field references.
   - `cmd/rimsky-publisher-conformance/{main.go, main_test.go, checks.go}` — the receiver path-matcher and the check-doc switch to the unified path. The CLI flag set may gain `--instance-id` (pick at write-plan).

9. **Source-doc cleanup:**
   - `runtime/message_delivery.go:11-12` docstring.
   - `graph/scheduler/scheduler.go:13` docstring.
   - `protocols/proto/v1/publisher.proto` file-level docstring (the regen propagates to generated bindings).

Stage 2 quiescence: post-cutover. Publisher protocol vocabulary is consistent; observation route is gone; `OnObservationSpec` is gone; bundled sensors POST through the unified endpoint; baseline schema matches the post-spec shape. Pre-spec deployments do not work; users must upgrade rimsky + bundled sensors together. Dev DBs need a wipe.

### Stage 3: State persistence + deploy + docs

1. Each of sensor-http / sensor-object-store / sensor-webhook gains a `state_db` env-var (`env:RIMSKY_SENSOR_<KIND>_STATE_DSN`); persists state to its own DB. Tests: restart preserves state.
2. sensor-cron's state-DB env-var is plumbed but in-memory mode remains the default; the in-memory cost is documented as "at most one MISSED fire per restart per publisher-subscription."
3. Fix the pre-existing docstring bug in `sensors/sensor-cron/sensor.go:17-19` ("at most one extra fire per restart per watch" → "at most one MISSED fire per restart per publisher-subscription"). Also rewrite the `sensors/sensor-cron/multi_replica_test.go` docstring per the cycle-4 finding (drop the "When the advisory-lock implementation lands" wording; reflect that single-replica is the v1 contract per `concept:replica`).
4. Dockerfiles for the four sensors (per-binary pattern; `sensors/sensor-<kind>/Dockerfile.sensor-<kind>`). `file:deploy/build-images.sh` extended.
5. `file:deploy/docker-compose.yml` extended with sensor services.
6. Helm chart deployment + service templates for each. `file:deploy/kubernetes/rimsky-chart/values.yaml` defaults.
7. `file:deploy/rimsky.yml` reference config: add top-level `publishers:` block with four entries declaring `protocols: [publisher]`. (Multi-protocol peers get duplicate entries across role-named blocks per §Architecture details.)
8. Concept doc updates: `concept:publisher` new (internal + public); `concept:publisher-subscription` new (internal + public); `concept:replica` new (internal + public); `concept:subscription` renamed → `concept:node-subscription` (internal + public) with full cross-reference sweep including in-code `@concept:` annotations; `concept:sensor` **end-to-end rewrite** (not refresh) per the cycle-4 finding; `concept:sensor-watch` deleted; `concept:message`, `concept:invalidate`, `concept:named-event`, `concept:backfill`, `concept:frame` refreshed; concept-catalog TOC (`.ok-planner/design/concepts.md` + public `docs/concepts/` index) updated to drop the old `subscription` slug and add `node-subscription`, `publisher-subscription`, `publisher`, `replica`. `docs/protocols/publisher.md` written (replacing any prior `docs/protocols/sensor.md`).
9. CLAUDE.md surgical edits per §File map.
10. CHANGELOG `## Unreleased` entry consolidating all three stages. Older `CHANGELOG.md` entries describing the retired sensor observation route remain unchanged as historical record; only the `## Unreleased` entry describes the unification + publisher rename.
11. Depguard allow-list extension for the three sensors that gain pgx-backed state DBs (`pgx-isolation` rule). `make lint` must pass.
12. `feature-index.md` edits: rename row 78 (`rimsky-sensor-conformance` → `rimsky-publisher-conformance`); delete row 81 (stale "Reference sensor binary (cron firing)" row pointing at the non-existent `cmd/rimsky-sensor-cron/` directory); sweep for `concept:subscription` → `concept:node-subscription` and `rimsky_sensor_watches` → `rimsky_publisher_subscriptions` references.
13. Delete the orphan empty `cmd/rimsky-sensor-cron/` directory (no binary lives there; the actual sensor is `pkg:sensors/sensor-cron/`).
14. `licensing.yml:~47` rename: `cmd/rimsky-sensor-conformance/` entry → `cmd/rimsky-publisher-conformance/`.

Stage 3 quiescence: feature-complete. The reference docker-compose stack starts the four sensors and they correctly post messages through the unified messages endpoint, persisting state where required.

## Verification

Each stage's verification per `code:.claude/rules/rules.md`:

```bash
cd /Users/patrick/Documents/projects/research/zonebase/submodules/rimsky
make build-all && make test-all && make lint && make license-lint
make proto-gen   # regenerate after Stage 2
go test ./runtime/... ./control/... ./foundation/... ./subscribers/... ./sensors/... -race -count=1
cd dashboards/rimsky-dashboard && npm test && npm run build
```

End-to-end smoke (after Stage 3):

```bash
cd deploy && docker compose up -d
curl http://localhost:8080/health
# Create a template with a publishers: block targeting sensor-cron
# Create an instance
# Wait for cron tick; observe rimsky_messages contains the message envelope from publisher
```

## Out of scope / deferred

- Multi-replica HA for bundled sensors beyond "operator can scale, but the reference impls advertise single-replica." Operator-facing docs are clear about this.
- Periodic resync from rimsky to detect publisher drift — not a rimsky concern per the architectural framing.
- Heartbeat protocol — not a rimsky concern.
- Advisory-lock primitives for publishers in foundation — not needed; publishers handle their own coordination if they want any.
- Shared `pkg:sensors/polling/` library — each sensor stays self-contained; the state shapes are different enough that forced sharing would create artificial coupling.
- TLS for publisher gRPC connections — post-v1 Plan C, same envelope as executors per `code:runtime/executor/client.go`.
- Non-sensor publisher implementations (e.g., a pub/sub-backed publisher). The protocol is general; no concrete non-sensor publisher exists today, and shipping one is its own design.

## Cross-cutting notes

- `@blessed-invariant 21` (blob bytes inert) extends naturally to message payloads. Publishers emitting raw observation bytes do not violate it because rimsky's message-delivery + attribute-substitution layers already treat payload bytes as inert per the existing invariant.
- `sender_kind: "publisher"` is the wire role at the rimsky boundary, regardless of whether the bundled implementation is a sensor or some future non-sensor publisher. This matches the `ClaimProducer` boundary: wire role is `claim_producer`, bundled service domain is `store`.
- Sensor state-DB configuration is env-var-only (`env:RIMSKY_SENSOR_<KIND>_STATE_DSN`); there is NO `cfg:rimsky.yml` key for sensor state DBs. The helm chart wires the env var via Deployment manifests.
- Existing operator-facing `route:POST /instances/{id}/messages` continues to default `sender_kind: "operator"` when not specified. The publisher pathway is opt-in via the explicit `sender_kind: "publisher"` value + `publisher_subscription_id`.
- The retention sweep for `rimsky_message_idempotencies` runs at the same cadence as `rimsky_claim_handle_retention`. The two sweeps are independently configurable via the `cfg:retention` block.
- Idempotency-key dedup is windowed by TTL: duplicates that arrive outside the 24h window dedup-miss and land as new messages. This matches the standard Stripe-style idempotency-key model. Operators wanting stronger guarantees set a longer TTL via `cfg:retention.message_idempotencies_trailing`.
- Peer declaration model: peers are declared under top-level role-named blocks in `rimsky.yml` (`claim_producers:`, `executors:`, `publishers:`, `validators:`, `data_processors:`). Each entry may include `protocols: [...]` to advertise the protocol it implements; a peer that implements multiple protocols gets duplicate entries across role-named blocks (same endpoint, different protocol advertisement per block). `code:control/config/publishers.go::DialPublisherAndValidationRegistries` walks the union of relevant maps and dispatches per advertised protocol.
- Stage 2's atomicity is intentional. The architectural unification is a single coherent change; splitting it across multiple stages creates partial-state intermediates that don't serve a migration-discipline need (the migration-flatten pass removed that need pre-v1). Pre-v1 break-freely; dev DBs are wiped on every release.

## Open items to resolve during write-plan

1. **`target_node` / `message_kind` column nullability**: spec recommends `target_node TEXT NOT NULL` (no empty-string sentinel; force template registration to fill it) and `message_kind TEXT NOT NULL DEFAULT 'invalidate'`. Confirm during write-plan whether any existing test fixture or template-DSL shape requires NULLability instead.
2. **`test/scenarios/sensor/` directory naming**: stay as `test/scenarios/sensor/` (these tests exercise the bundled sensor implementations specifically) or migrate to `test/scenarios/publisher/` for protocol-level test coverage. Recommendation: keep `test/scenarios/sensor/`; introduce a new `test/scenarios/publisher/` only if/when non-sensor publisher implementations land. Pick at write-plan.
3. **Sensor-cron docstring fix**: `code:sensors/sensor-cron/sensor.go:17-19` carries a pre-existing inaccuracy ("at most one extra fire per restart per watch" — should be "at most one MISSED fire per restart per publisher-subscription"). Lands in Stage 3. Tracked here so it doesn't get lost across the spec → plan → execution boundary. (Implementation work, not a spec-text change.)
4. **Capability-check rejection status code**: pick `403 Forbidden` or `404 Not Found` for unknown / cross-instance / inactive `publisher_subscription_id`. Spec recommends `403` (more honest about "I see the request but you're not authorized"); pick at write-plan unless write-plan finds a reason against.
5. **Conformance binary `--instance-id` CLI flag**: the new path is instance-scoped (`/instances/<id>/messages`), so the conformance binary's fake-rimsky-endpoint setup may need an `--instance-id` flag (vs. inventing one on the fly). Pick at write-plan.
6. **`payload_template` canonicalizer probe**: verify (via a small write-plan-time probe test) whether `graph/template/canonical/*.go` currently rejects unknown fields strictly or laxly. If strict: the `payload_template` rejection at registration is free behavior — the canonicalizer already rejects unknown keys; the spec's added work is purely the operator-facing error message. If lax: additional canonicalizer work is needed to add the explicit reject + the documented error message. Probe outcome determines whether Stage 2 substep 4 (canonicalizer rejection) is a one-liner or a multi-line change.

## Estimated change footprint

Refined estimate (cycle-4 grep audit of the protocol-identifier rename surface):

- The protocol-identifier rename surface ("Sensor" / "Watch" / `sensor_` / `watch_id` and their case variants tied to the Sensor protocol) touches **~449 hits across ~30 Go files**, excluding generated `*.pb.go` files. Mechanical rename + regen handles most of this via `goland-rename` / `gopls rename` / sed; ~5 hand-edits per file are expected on test fixtures and field constructors. Generated `*.pb.go` files are regenerated by `make proto-gen`, not hand-edited.
- The publisher-subscription concept doc + node-subscription concept rename adds ~20 markdown file touches (cross-references across concept docs, in-code `@concept:` annotations, `feature-index.md`, `CLAUDE.md`).
- **Realistic file totals**:
  - **~60-75 files modified**: the protocol rename surface, the receiver-side concept rename, the message-endpoint capability check, the schema-baseline rewrite, the four bundled-sensor URL + body updates, the per-sensor unit test path-matchers, the build/deploy artifact updates, the CLAUDE.md surgical edits, the `licensing.yml` entry, `feature-index.md`. The line-count delta is dominated by the deletions (`sensors.go` + `sensors_test.go` ~480 lines; `OnObservationSpec` + alias; observation route + handler) plus rename mechanics (gofmt-clean rename, not new logic).
  - **~12-15 files created**: 4 sensor Dockerfiles, 8 helm template files (4 deployment + 4 service), 3 state-DB modules (sensor-http / object-store / webhook), 3 internal concept docs (publisher, publisher-subscription, replica), 3 public concept docs (publisher, publisher-subscription, replica), 1 public protocol guide (`docs/protocols/publisher.md`). New `cmd/rimsky-publisher-conformance/` directory + 3 files inside; new `runtime/publishers.go`, `runtime/clientiface/publisher.go`, `runtime/remote/publisher_client.go`, `control/config/publishers.go`, `foundation/persistence/publisher_subscriptions.go` and 2 driver impls. Many of these are renames of existing files; the "created" tag in §File map captures the new content under the new path.
  - **~6 files / directories deleted (entire-file deletions)**: `control/controlapi/sensors.go`, `control/controlapi/sensors_test.go`, `protocols/proto/v1/sensor.proto` (+ regenerated bindings deleted by regen), `.ok-planner/design/concepts/sensor-watch.md`, the orphan `cmd/rimsky-sensor-cron/` directory entirely, and the old `.ok-planner/design/concepts/subscription.md` (replaced by the renamed `node-subscription.md`). The `OnObservationSpec` Go type and its alias are deleted; the routing fields move inline into the proto.
- 0 enumerated migrations: schema changes land via direct baseline edits, per the migration-flatten housekeeping discipline. Per-sensor state-DB schema bootstrap is `CREATE TABLE IF NOT EXISTS` at sensor startup, not a foundation migration.

Surface size is bigger than the prior draft estimate, dominated by the publisher rename sweep. The architectural change itself is moderate; the rename mechanics scale with the existing reference-density of "Sensor" / "Watch" in the codebase. The net code change is negative — most touches are deletions or mechanical renames.
