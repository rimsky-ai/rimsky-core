---
concept: sensor
status: as-is
aliases: []
references:
  - ../../specs/2026-05-17-sensor-messaging-unification-design.md
---

# Sensor

## Definition

A sensor is a class of `concept:publisher` implementation that observes external state. Sensors poll, listen, or otherwise watch some out-of-rimsky substrate (clock, HTTP endpoint, object-store prefix, webhook port) and publish messages into rimsky when the watched substrate changes.

Sensors implement the `concept:publisher` protocol — `Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions` — and POST message envelopes to the generic `POST /instances/{instance_id}/messages` endpoint with `sender_kind: "publisher"` + `publisher_subscription_id` capability token.

The bundled reference impls under `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/` are sensors-by-construction; they share no protocol-level surface with rimsky beyond the Publisher protocol itself.

## Purpose

To bridge external substrate changes into rimsky's instance frames without requiring rimsky-core knowledge of the substrate. A sensor observes the substrate, builds an opaque payload, and hands it to rimsky as a generic `concept:message`; rimsky routes the message through the existing cascade machinery.

## Boundaries

Owns: the watching loop, the substrate dialect (cron expression, HTTP poll, object-store list), the in-binary per-subscription state (next fire time, body hash, watermark cursor, last idempotency key), and the message-envelope construction at fire time.

Does NOT own: the wire protocol (that's `concept:publisher`), the message envelope shape (that's `concept:message`), the per-instance binding state (that's `concept:publisher-subscription`, stored in `table:rimsky_publisher_subscriptions`), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher` (sensors implement it), `concept:publisher-subscription` (sensors hold its publisher-side state in their own per-binary state DB), `concept:message` (sensors emit them), `concept:replica` (sensor binaries are single-replica per v1 contract).

## Invariants

- Sensors are deployed as standalone services advertised in the `publishers:` block of `cfg:rimsky.yml`. Same deployment model as `concept:claim-producer` or `concept:executor`.
- Templates declare sensors via the `publishers:` block (the same block; sensors ARE publishers); at instance creation, rimsky resolves each publisher entry's config via `{{params.X}}` substitution and calls `Publisher.Subscribe`.
- At instance termination, rimsky calls `Publisher.Unsubscribe` for each registered publisher-subscription.
- Each emit constructs a message envelope `{kind, target, payload, sender, sender_kind: "publisher", publisher_subscription_id}` and POSTs to `/instances/{instance_id}/messages` with an `Idempotency-Key` header. Inert payload per `@blessed-invariant: messages are inert in rimsky`.
- Sensors observe; they do not interpret. Payload bytes flow through rimsky unread until a consumer's substitution leaf walks into them.
- Single-replica per `concept:replica` — operators run one pod per sensor binary; rimsky does not coordinate multi-replica fan-in.

## Annotation sites

- `code:protocols/proto/v1/publisher.proto` — protobuf surface (shared with all publishers; sensors are one class).
- `code:github.com/fallguyconsulting/rimsky-services/sensors/sensor-cron/`, `code:github.com/fallguyconsulting/rimsky-services/sensors/sensor-http/`, `code:github.com/fallguyconsulting/rimsky-services/sensors/sensor-object-store/`, `code:github.com/fallguyconsulting/rimsky-services/sensors/sensor-webhook/` — bundled reference impls.
- `code:cmd/rimsky-publisher-conformance/` — conformance suite.

## Notes

Introduced as a service kind by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` (then named the `Sensor` protocol). The 2026-05-17 unification collapses the rimsky-side observation-deposit route into the generic messages endpoint and renames the protocol from `Sensor` to `Publisher`. Sensors remain a named class of publisher implementation — their identity, naming, and bundled implementations are unchanged at the binary boundary.

The bundled `sensor-cron` replaces the retired per-node `schedule:` field — cron becomes a publisher kind, not a rimsky-core concept. Its missed-fire policy ("at most one MISSED fire per restart per publisher-subscription") preserves the freshness-over-backfill semantic of the retired scheduler-tick cron path.

V1 deferred: `sensor-sql` (substrate/connection/query surface complex), `sensor-kafka` (heavy dependency).

2026-05-24: bundled sensor reference impls moved to pkg:github.com/fallguyconsulting/rimsky-services. Path references updated. See spec 2026-05-24-repo-reorganization-design phase P3.
