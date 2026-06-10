---
concept: event-log
status: as-is
aliases:
  - audit log
---

# Event log (audit log)

## What it is

Rimsky's internal append-only audit-log ledger. Each row carries an auto-incrementing id, the originating instance and node, a typed `kind` value (operational kinds drawn from a proto-declared enum; signal-class kinds carrying canonical signal type-paths), a JSON `payload`, and an `occurred_at` timestamp. Indexed for lookup by node, by instance, and by kind, each ordered newest-first. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the operator-dashboard event-feed endpoint that `cascade-graph` exposes, and — for the `auth.*` subset — by a dedicated audit-read surface gated on the `audit:read` action (see `concept:permission`), which filters those rows by actor, action, target, result, mode, and time.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB, not bound by `@blessed-invariant 21` opacity). Adding a new operational kind = adding an enum value in the events proto + regenerating Go bindings (no schema migration; the storage column stays `TEXT`). Rimsky's app logic consumes typed values exclusively, never raw strings, so typo-induced silent observability blind spots are prevented at the app boundary.

## Boundaries

Owns: the audit-log schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the named-event ledger (see `named-event` "Ledger storage" subsection), the trace-retention window value (a shared per-instance bound that also governs frames and node_runs, applied here as a reaping cutoff), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from the event feed), `observability`, `named-event` (sibling append-only ledger with a different opacity discipline).

## Invariants

- The `kind` value is typed at rimsky's app boundary: operational kinds via the proto-declared `OperationalKind` enum (see `decision:event-log-kind-enum`); signal-class kinds via the parsed signal type-path. The persistence column stays `TEXT` for marshaling flexibility — no `CHECK` constraint, because the enum at the app boundary IS the gate (unknown strings at the unmarshal boundary are defensive errors, not control-flow inputs).
- The `payload` is rimsky's own JSON — readable by rimsky for the dashboard and audit consumers. NOT bound by `@blessed-invariant 21` (which governs the named-event ledger).
- Audit rows are reaped under the shared trailing trace-retention window (the same per-instance window that bounds frames and node_runs), in addition to cascade-removal on instance delete; within the window the log is append-only.
- **Writes are never *silently* dropped; under a healthy backing store they are durable.** The log is the canonical forensic record. The per-request auth-audit write (`auth.access_attempted`) is **synchronous in the request path** — written inline after the handler returns (so `response_status` and `duration_ms` are known) and before the gate returns, not through a best-effort queue that drops under load. Under a healthy store every intended row is persisted before the request completes. The honest limit of the current implementation: the synchronous write is bounded by a short deadline, and a write that fails or exceeds it is **surfaced (logged at error), not retried or buffered** — so a backing-store outage or stall can lose that row (with an operator-visible error, never a silent discard), and a degraded store spends request-path latency rather than dropping. So the guarantee is *never silently dropped* and *durable under normal operation* — not *always persisted under all conditions*. Operational event rows (node transitions, lock/work events) are written synchronously inside the supervisor's transactions, so they are durable as part of the mutation that produced them.

## Auth event kinds

Five `auth.*` event kinds capture the control-plane auth surface. They share the same `(kind, payload)` shape as every other audit-log row. All five are readable through the `audit:read`-gated audit surface, which filters on the `auth.*` payload fields (actor `key_id` / `key_name`, `action`, target path, `response_status`, `mode`) plus the time range.

- `auth.access_attempted` — emitted by the control-plane auth middleware's per-action gate after every authenticated request runs. Payload includes `key_id`, `key_name`, `identity_kind`, `protocol_skin` (`http` | `mcp`), `action`, `request_path`, `request_method`, `request_params` (verbatim), `response_status`, `mode` (`execute` | `dry_run`), `executed` (bool), `duration_ms`, `client_ip`, `user_agent`.
- `auth.access_denied` — emitted on 401 / 403. Same shape plus a `denial_reason` enum: `no_token | invalid_token | expired_token | revoked_token | permission_denied`. For pre-action-resolution denials (the first four) `action`, `request_params`, `mode` are null; for `permission_denied` they are populated.
- `auth.key_created` — emitted by the control-plane key-creation handler. Payload: `key_id`, `key_name`, `permissions`, `created_by_key_id`, `expires_at`.
- `auth.key_revoked` — emitted by the control-plane key-revocation handler and the runtime rotation-grace sweep. Payload: `key_id`, `key_name`, `revoked_by_key_id`, `reason` (`manual | rotation_grace | expired`).
- `auth.key_rotated` — emitted by the control-plane key-rotation handler. Payload: `key_id` and `key_name` (the new / surviving key, so the actor filter surfaces a rotation like every other auth row), `old_key_id`, `new_key_id`, `name`, `revoke_at`.
