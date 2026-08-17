---
concept: event-log
aliases:
  - audit log
---

# Event log (audit log)

## What it is

Rimsky's internal append-only audit-log ledger. Each row carries an auto-incrementing id, the originating instance and node when the row is instance/node-scoped (both columns are optional — rows from surfaces outside a running instance, such as the `auth.*` kinds, carry neither), a typed `kind` value (operational kinds drawn from a proto-declared enum; signal-class kinds carrying canonical signal type-paths), a JSON `payload`, and an `occurred_at` timestamp. Indexed for lookup by node, by instance, and by kind, each ordered newest-first. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the operator-dashboard event-feed endpoint that `cascade-graph` exposes, and — for the `auth.*` subset — by a dedicated audit-read surface gated on the `audit:read` action (see `concept:permission`), which filters those rows by actor, action, target, result, mode, and time.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB). Adding a new operational kind = adding an enum value in the events proto, regenerating Go bindings, and registering the kind's wire-form string (no schema migration; the storage column stays `TEXT`). Rimsky's app logic consumes typed values exclusively, never raw strings, so typo-induced silent observability blind spots are prevented at the app boundary.

## Boundaries

Owns: the audit-log schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the trace-retention window value (a single deployment-wide bound that also governs frames and node_runs, applied here as a time-based reaping cutoff), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from the event feed), `observability`.

## Invariants

- The `kind` value is typed at rimsky's app boundary: operational kinds via the proto-declared `OperationalKind` enum (see `decision:event-log-kind-enum`); signal-class kinds via the parsed signal type-path. The persistence column stays `TEXT` for marshaling flexibility — no `CHECK` constraint, because the enum at the app boundary IS the gate (unknown strings at the unmarshal boundary are defensive errors, not control-flow inputs).
- The `payload` is rimsky's own JSON — readable by rimsky for the dashboard and audit consumers — and its shape is declared in the events proto, covering both operational and signal-class rows. One message may serve several kinds where the kind varies and the shape does not; the terminal-error family is the structural case, an open-ended set of error classes sharing one message by construction. Rimsky constructs every payload from the generated type; a payload is never assembled as an untyped map, so a declared field with no writer and a written key with no declaration are both unrepresentable. Fields whose shape belongs to someone else — an executor's error data, a template author's opaque blob — are never given a rimsky shape: they are declared as a generic JSON value where `concept:inertness` classifies the stream structurally inert (so it stays traversable at that concept's sanctioned read sites), and as bytes where it classifies the stream byte-opaque. Either way they pass through uninspected.
- Audit rows are reaped under the shared trailing trace-retention window (the same deployment-wide, time-based window that bounds frames and node_runs; event rows age out by their `occurred_at` time only, unlike the per-instance recent-frames-kept count cap that bounds structural rows), in addition to cascade-removal on instance delete; within the window the log is append-only.
- A key's expiry is a passive per-request check against the key row's expiry timestamp; it never itself emits `auth.key_revoked` — revocation events are explicit (manual or rotation-grace-driven) only.
- A permission-denied access-denied row populates the shared mode field with the request's resolved mode, so the audit surface's mode filter distinguishes a dry-run probe from an enforcement denial. Denials raised before permission evaluation (a missing, invalid, expired, or revoked token) carry no mode — none was resolved.
- `auth.key_rotated` names the new/surviving key as the row's actor fields, not the old key, so the `audit:read` surface's actor filter surfaces a rotation like every other auth row.
- **Writes are never *silently* dropped; under a healthy backing store they are durable.** The log is the canonical forensic record. The per-request auth-audit write (`auth.access_attempted`) is **synchronous in the request path** — written inline after the handler returns (so `response_status` and `duration_ms` are known) and before the gate returns, not through a best-effort queue that drops under load. Under a healthy store every intended row is persisted before the request completes. The honest limit: the synchronous write is bounded by a short deadline, and a write that fails or exceeds it is **surfaced (logged at error), not retried or buffered** — so a backing-store outage or stall can lose that row (with an operator-visible error, never a silent discard), and a degraded store spends request-path latency rather than dropping. So the guarantee is *never silently dropped* and *durable under normal operation* — not *always persisted under all conditions*. Operational event rows (node transitions, lock/work events, administrative and read-surface audit rows) are written synchronously inside the transaction of the writer that produced them — supervisor, scheduler, or control-api — so they are durable as part of the mutation that produced them.

## Auth event kinds

Five `auth.*` event kinds capture the control-plane auth surface — one each for an attempted access, a denied access, key creation, key revocation, and key rotation. Each shares the same `(kind, payload)` shape as every other audit-log row and the same general actor/action/target/result/mode payload shape; per-kind payload field membership is owned by the emission code, not enumerated here. All five are readable through the `audit:read`-gated audit surface, which filters on those shared fields plus the time range.
