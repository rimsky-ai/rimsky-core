---
concept: event-log
status: as-is
aliases:
  - audit log
  - rimsky_events table
references:
  - _discover/2026-05-10-event-log-append-only-jsonb.md
---

# Event log (audit log)

## What it is

`rimsky_events` — rimsky's internal append-only audit log. Schema: `id BIGSERIAL`, `instance_id`, `node_id`, `kind TEXT` (free-form, no enum CHECK), `payload JSONB`, `occurred_at TIMESTAMPTZ`. Indexed by `(node_id, occurred_at DESC)`, `(instance_id, ...)`, `(kind, ...)`. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the `/events` route in `cascade-graph` for the operator dashboard.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB, not bound by `@blessed-invariant 21` opacity). The free-form `kind` column lets new event categories appear with zero migration; the price is that typos produce events no consumer finds.

## Boundaries

Owns: the `rimsky_events` schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the named-event ledger (`rimsky_node_events` — see `named-event` "Ledger storage" subsection), retention policy (operator-managed), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from `/events`), `observability`, `named-event` (sibling append-only table with different opacity discipline).

## Invariants

- `rimsky_events.kind` is free-form; no enum CHECK. Zero-migration to add a new kind; typos produce events no consumer finds.
- `rimsky_events.payload` is rimsky's own JSONB — readable by rimsky for the dashboard and audit consumers. NOT bound by `@blessed-invariant 21` (which governs the named-event ledger).
- No built-in retention; operator-managed retention is required.

## Aliases and historical names

Pre-`2026-05-11-design-log-convergence`, this concept also covered `rimsky_node_events` (named-event ledger). That material moved to `concepts/named-event.md` "Ledger storage" subsection. Filename `event-log.md` retained; content is now audit-log-only.

Post-2026-05-15: `rimsky_events` remains the audit log for **events** (executor emissions, state transitions, error classifications). The new **messages** primitive (`concept:message`) has its own audit table `rimsky_messages` with operational columns (`kind`, `sender`, `sender_kind`, `target`, `payload`, `delivered_at`, `frame_id`, `cancelled`, `backfill_operation_id`). The two tables are siblings — events are internal-to-rimsky and frame-synchronous; messages are boundary-crossing and frame-bounded. See `concept:message`, `concept:named-event`.

## Auth event kinds (added 2026-05-15)

The control-plane MCP and auth spec (`.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`) adds five `auth.*` event kinds. They share the same `(kind, payload)` shape as every other row in `rimsky_events` — no schema change.

- `auth.access_attempted` — emitted by `code:control/controlapi/auth_middleware.go::AuthState.gateByAction` after every authenticated request runs. Payload includes `key_id`, `key_name`, `identity_kind`, `protocol_skin` (`http` | `mcp`), `action`, `request_path`, `request_method`, `request_params` (verbatim), `response_status`, `mode` (`execute` | `dry_run`), `executed` (bool), `duration_ms`, `client_ip`, `user_agent`.
- `auth.access_denied` — emitted on 401 / 403. Same shape plus a `denial_reason` enum: `no_token | invalid_token | expired_token | revoked_token | permission_denied`. For pre-action-resolution denials (the first four) `action`, `request_params`, `mode` are null; for `permission_denied` they are populated.
- `auth.key_created` — emitted by `code:control/controlapi/auth_handlers.go::handleCreateKey`. Payload: `key_id`, `key_name`, `permissions`, `created_by_key_id`, `expires_at`.
- `auth.key_revoked` — emitted by `code:control/controlapi/auth_handlers.go::handleRevokeKey` and the rotation-grace sweep `code:runtime/auth_sweep.go::SweepRotationGrace`. Payload: `key_id`, `key_name`, `revoked_by_key_id`, `reason` (`manual | rotation_grace | expired`).
- `auth.key_rotated` — emitted by `code:control/controlapi/auth_handlers.go::handleRotateKey`. Payload: `old_key_id`, `new_key_id`, `name`, `revoke_at`.

## Notes

- [2026-05-15] `auth.*` event kinds added by spec `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`.

## Open within this concept

- `rimsky_events.kind` is free-form — see `tensions/events-kind-no-enum.md`.
