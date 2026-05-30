---
concept: event-log
status: as-is
aliases:
  - audit log
---

# Event log (audit log)

## What it is

Rimsky's internal append-only audit-log ledger. Each row carries an auto-incrementing id, the originating instance and node, a free-form `kind` text column (no enum constraint), a JSON `payload`, and an `occurred_at` timestamp. Indexed for lookup by node, by instance, and by kind, each ordered newest-first. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the operator-dashboard event-feed endpoint that `cascade-graph` exposes, and — for the `auth.*` subset — by a dedicated audit-read surface gated on the `audit:read` action (see `concept:permission`), which filters those rows by actor, action, target, result, mode, and time.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB, not bound by `@blessed-invariant 21` opacity). The free-form `kind` column lets new event categories appear with zero migration; the price is that typos produce events no consumer finds.

## Boundaries

Owns: the audit-log schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the named-event ledger (see `named-event` "Ledger storage" subsection), retention policy (operator-managed), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from the event feed), `observability`, `named-event` (sibling append-only ledger with a different opacity discipline).

## Invariants

- The `kind` column is free-form; no enum constraint. Zero-migration to add a new kind; typos produce events no consumer finds.
- The `payload` is rimsky's own JSON — readable by rimsky for the dashboard and audit consumers. NOT bound by `@blessed-invariant 21` (which governs the named-event ledger).
- No built-in retention; operator-managed retention is required.
- **Writes are never *silently* dropped; under a healthy backing store they are durable.** The log is the canonical forensic record. The per-request auth-audit write (`auth.access_attempted`) is **synchronous in the request path** — written inline after the handler returns (so `response_status` and `duration_ms` are known) and before the gate returns, not through a best-effort queue that drops under load. Under a healthy store every intended row is persisted before the request completes. The honest limit of the current implementation: the synchronous write is bounded by a short deadline, and a write that fails or exceeds it is **surfaced (logged at error), not retried or buffered** — so a backing-store outage or stall can lose that row (with an operator-visible error, never a silent discard), and a degraded store spends request-path latency rather than dropping. So the guarantee is *never silently dropped* and *durable under normal operation* — not *always persisted under all conditions*. Operational event rows (node transitions, lock/work events) are written synchronously inside the supervisor's transactions, so they are durable as part of the mutation that produced them.

## Aliases and historical names

Pre-`spec:2026-05-11-design-log-convergence`, this concept also covered the named-event ledger. That material moved to `named-event`'s "Ledger storage" subsection. The concept name `event-log` is retained; content is now audit-log-only.

Post-2026-05-15: the audit log remains the record for **events** (executor emissions, state transitions, error classifications). The new **messages** primitive (`concept:message`) has its own audit ledger with operational columns (kind, sender, sender-kind, target, payload, delivered-at, frame id, cancelled flag, backfill-operation id). The two ledgers are siblings — events are internal-to-rimsky and frame-synchronous; messages are boundary-crossing and frame-bounded. See `concept:message`, `concept:named-event`.

## Auth event kinds (added 2026-05-15)

The control-plane MCP and auth spec (`spec:2026-05-15-control-plane-mcp-and-auth`) adds five `auth.*` event kinds. They share the same `(kind, payload)` shape as every other audit-log row — no schema change. All five are readable through the `audit:read`-gated audit surface, which filters on the `auth.*` payload fields (actor `key_id` / `key_name`, `action`, target path, `response_status`, `mode`) plus the time range.

- `auth.access_attempted` — emitted by the control-plane auth middleware's per-action gate after every authenticated request runs. Payload includes `key_id`, `key_name`, `identity_kind`, `protocol_skin` (`http` | `mcp`), `action`, `request_path`, `request_method`, `request_params` (verbatim), `response_status`, `mode` (`execute` | `dry_run`), `executed` (bool), `duration_ms`, `client_ip`, `user_agent`.
- `auth.access_denied` — emitted on 401 / 403. Same shape plus a `denial_reason` enum: `no_token | invalid_token | expired_token | revoked_token | permission_denied`. For pre-action-resolution denials (the first four) `action`, `request_params`, `mode` are null; for `permission_denied` they are populated.
- `auth.key_created` — emitted by the control-plane key-creation handler. Payload: `key_id`, `key_name`, `permissions`, `created_by_key_id`, `expires_at`.
- `auth.key_revoked` — emitted by the control-plane key-revocation handler and the runtime rotation-grace sweep. Payload: `key_id`, `key_name`, `revoked_by_key_id`, `reason` (`manual | rotation_grace | expired`).
- `auth.key_rotated` — emitted by the control-plane key-rotation handler. Payload: `key_id` and `key_name` (the new / surviving key, so the actor filter surfaces a rotation like every other auth row), `old_key_id`, `new_key_id`, `name`, `revoke_at`.

## Notes

- [2026-05-15] `auth.*` event kinds added by `spec:2026-05-15-control-plane-mcp-and-auth`.
- 2026-05-23 — Per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling`: the node-run-transition subset of the audit log's `kind` values now carries canonical signal type-paths (e.g., `terminal/error/http/timeout`) rather than free-form strings; for those rows `payload` carries the signal payload per its type's schema (`concept:signal`). Other audit kinds (`state_transition`, `lock_*`, `work_*`, `auth.*`, etc.) continue to use free-form text — see `tension:events-kind-no-enum` partial-coverage note.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-29 — Per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`: the per-request auth-audit write (`auth.access_attempted`) is now **durable and synchronous** in the request path, replacing the prior best-effort, droppable-under-load async dispatcher (added the durability invariant above). Adds an `audit:read`-gated read surface over the `auth.*` rows, filterable by actor / action / target / result / mode / time — recorded as a reader of those rows in "What it is" and the auth-event-kinds section.
- 2026-05-30 — Tightened the durability invariant to match the implementation. The 2026-05-29 wording (per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`) claimed the auth-audit row is "always persisted, never discarded under load." In fact the synchronous write is bounded by a short deadline and, on failure or timeout, logs at error and drops the row rather than retrying or buffering — so the honest guarantee is *never silently dropped; durable under a healthy store; a store outage or stall costs request latency and can lose the row with an operator-visible error*. A durable local-spool write path (local append + background batched ship to the backing store) is sketched as the intended way to close the gap to *always persisted*; it is pre-spec and not yet adopted. Also recorded that `auth.key_rotated` now carries uniform `key_id` / `key_name` (the new key) so the `audit:read` actor filter surfaces rotations like every other auth row.

## Open within this concept

- The audit log's `kind` column is free-form — see `tension:events-kind-no-enum`.
