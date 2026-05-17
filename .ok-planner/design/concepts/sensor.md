---
concept: sensor
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Sensor

## Definition

A new service kind (fifth alongside executor / claim-producer / lifecycle-subscriber / blob-backend). First-class in-instance services that run continuously, monitor external state, and push messages to their instance. Declared in `rimsky.yml`'s `sensors:` block and bound per-instance via the template's `sensors:` block.

Protocol methods:

- **`Capabilities() → {supported_kinds, config_schemas}`** — advertises which `kind` of watching this sensor supports and the config-schema per kind.
- **`StartWatch(watch_id, instance_id, kind, config) → ack`** — begin a watch.
- **`StopWatch(watch_id) → ack`** — stop a watch.
- **`ListWatches() → [watch_descriptor]`** — for rimsky-side resync after sensor restart.

Observation delivery: sensors push to rimsky via the control-api `POST /sensors/{watch_id}/observations` endpoint with `{payload}`. Rimsky resolves `watch_id` to `(instance, target_node, message_kind)` from its sensor-state registry (populated at `StartWatch`), constructs a message envelope, and enqueues it in `rimsky_messages`.

## Boundaries

Owns: the `Sensor` gRPC protocol surface, the `rimsky_sensor_watches` table (rimsky-side watch state), the `POST /sensors/{watch_id}/observations` control-api endpoint, the resync flow via `ListWatches`. Does NOT own: the substrate watching (lives in the sensor binary), per-sensor config DSL (per-kind config_schema; parsed sensor-side). Adjacent: `concept:service`, `concept:message`, `concept:claim-producer`, `concept:executor`.

## Invariants

- Sensors are deployed externally as standalone services (separate processes), advertised in `rimsky.yml` — same deployment model as ClaimProducer / Executor.
- Templates declare sensors in a `sensors:` block per-template; at instance creation, rimsky resolves each sensor's config via substitution from instance params and calls `StartWatch`.
- At instance termination, rimsky calls `StopWatch(watch_id)` for each registered watch.
- Each observation arrival constructs a message envelope with `sender = sensor_name`, `sender_kind = sensor`, `target = target_node`, `kind = message_kind`. Inert payload per `@blessed-invariant 24` (messages-inert).
- Sensor state on the rimsky side lives in `rimsky_sensor_watches`; sensor-side state can be reconstructed via `ListWatches` resync (rimsky compares its expected set against the sensor's reported set and re-issues `StartWatch` for any missing).

## Annotation sites

- `code:protocols/proto/v1/sensor.proto` — protobuf surface.
- `code:sensors/sensor-cron/`, `code:sensors/sensor-http/`, `code:sensors/sensor-object-store/`, `code:sensors/sensor-webhook/` — bundled reference impls.
- `code:control/controlapi/sensors.go` — `POST /sensors/{watch_id}/observations` handler.
- `code:cmd/rimsky-sensor-conformance/` — conformance suite.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The bundled `sensor-cron` replaces the retired per-node `schedule:` field — cron becomes a sensor kind, not a rimsky-core concept. The `sensor-cron`'s `missed_fires: drop` config preserves the "missed fires NOT backfilled" semantic of the retired scheduler-tick cron path.

V1 deferred: `sensor-sql` (substrate/connection/query surface complex), `sensor-kafka` (heavy dependency).
