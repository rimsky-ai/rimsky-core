---
concept: event-log
status: as-is
aliases:
  - audit log
references:
  - _discover/2026-05-10-event-log-append-only-jsonb.md
---

# Event log (audit log)

## What it is

Rimsky's internal append-only audit-log ledger. Each row carries an auto-incrementing id, the originating instance and node, a free-form `kind` text column (no enum constraint), a JSON `payload`, and an `occurred_at` timestamp. Indexed for lookup by node, by instance, and by kind, each ordered newest-first. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the operator-dashboard event-feed endpoint that `cascade-graph` exposes.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB, not bound by `@blessed-invariant 21` opacity). The free-form `kind` column lets new event categories appear with zero migration; the price is that typos produce events no consumer finds.

## Boundaries

Owns: the audit-log schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the named-event ledger (see `named-event` "Ledger storage" subsection), retention policy (operator-managed), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from the event feed), `observability`, `named-event` (sibling append-only ledger with a different opacity discipline).

## Invariants

- The `kind` column is free-form; no enum constraint. Zero-migration to add a new kind; typos produce events no consumer finds.
- The `payload` is rimsky's own JSON — readable by rimsky for the dashboard and audit consumers. NOT bound by `@blessed-invariant 21` (which governs the named-event ledger).
- No built-in retention; operator-managed retention is required.

## Aliases and historical names

Pre-`spec:2026-05-11-design-log-convergence`, this concept also covered the named-event ledger. That material moved to `named-event`'s "Ledger storage" subsection. The concept name `event-log` is retained; content is now audit-log-only.

Post-2026-05-15: the audit log remains the record for **events** (executor emissions, state transitions, error classifications). The new **messages** primitive (`concept:message`) has its own audit ledger with operational columns (kind, sender, sender-kind, target, payload, delivered-at, frame id, cancelled flag, backfill-operation id). The two ledgers are siblings — events are internal-to-rimsky and frame-synchronous; messages are boundary-crossing and frame-bounded. See `concept:message`, `concept:named-event`.

## Auth event kinds (added 2026-05-15)

The control-plane MCP and auth spec (`spec:2026-05-15-control-plane-mcp-and-auth`) adds five `auth.*` event kinds. They share the same `(kind, payload)` shape as every other audit-log row — no schema change.

- `auth.access_attempted` — emitted by the control-plane auth middleware's per-action gate after every authenticated request runs. Payload includes `key_id`, `key_name`, `identity_kind`, `protocol_skin` (`http` | `mcp`), `action`, `request_path`, `request_method`, `request_params` (verbatim), `response_status`, `mode` (`execute` | `dry_run`), `executed` (bool), `duration_ms`, `client_ip`, `user_agent`.
- `auth.access_denied` — emitted on 401 / 403. Same shape plus a `denial_reason` enum: `no_token | invalid_token | expired_token | revoked_token | permission_denied`. For pre-action-resolution denials (the first four) `action`, `request_params`, `mode` are null; for `permission_denied` they are populated.
- `auth.key_created` — emitted by the control-plane key-creation handler. Payload: `key_id`, `key_name`, `permissions`, `created_by_key_id`, `expires_at`.
- `auth.key_revoked` — emitted by the control-plane key-revocation handler and the runtime rotation-grace sweep. Payload: `key_id`, `key_name`, `revoked_by_key_id`, `reason` (`manual | rotation_grace | expired`).
- `auth.key_rotated` — emitted by the control-plane key-rotation handler. Payload: `old_key_id`, `new_key_id`, `name`, `revoke_at`.

## Notes

- [2026-05-15] `auth.*` event kinds added by `spec:2026-05-15-control-plane-mcp-and-auth`.
- 2026-05-23 — Per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling`: the node-run-transition subset of the audit log's `kind` values now carries canonical signal type-paths (e.g., `terminal/error/http/timeout`) rather than free-form strings; for those rows `payload` carries the signal payload per its type's schema (`concept:signal`). Other audit kinds (`state_transition`, `lock_*`, `work_*`, `auth.*`, etc.) continue to use free-form text — see `tension:events-kind-no-enum` partial-coverage note.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

## Open within this concept

- The audit log's `kind` column is free-form — see `tension:events-kind-no-enum`.
