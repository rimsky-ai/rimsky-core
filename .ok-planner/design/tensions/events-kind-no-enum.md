---
tension: events-kind-no-enum
category: unspecified
status: open
affects:
  - event-log
---

# `rimsky_events.kind` is free-form (no enum CHECK); kind typos produce events no consumer finds

## What is muddy

`rimsky_events.kind` is `TEXT NOT NULL` with no enum CHECK constraint. New event kinds are zero-migration: write a new string + matching JSONB payload shape. `proto/v1/events.proto` defines payload shapes for some kinds but is not enforced at write time.

The trade-off is documented: zero migration cost for new kinds vs. no schema enforcement. A typo in a `kind` string ("started" vs "starteed") produces an audit-log entry no consumer finds by canonical name.

## Why it matters

Observability blind spots accumulate silently. A future "kind catalog" tool can't be authoritative because the catalog isn't enforced.

## Resolution candidates (do NOT pick)

- Add a CHECK constraint enumerating valid kinds.
- Promote `kind` to a foreign key into a `rimsky_event_kinds` registry table.
- Lint at write time using `events.proto`'s defined kinds as the registry.

## Evidence

- `_discover/2026-05-10-event-log-append-only-jsonb.md` Observations bullets 2-3.

## Notes

- 2026-05-23 — Partially addressed by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Node-run-transition `kind` values are now standardized under the signal type-path taxonomy (`concept:signal`): `terminal/*`, `transient/*`, `attribute/*`, `event/*`, `message/*`, validated at registration. Non-signal audit kinds (`state_transition`, `lock_acquired`, `work_started`, `attributes_substituted`, `auth.*`, etc.) remain free-form `TEXT`; a separate spec would need to taxonomize them. Tension does not move to `_resolved/`.
