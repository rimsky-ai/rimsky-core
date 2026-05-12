---
tension: events-table-name-overlap
category: overloaded
status: resolved
affects:
  - event-log
  - named-event
resolution:
  shape: superseded
  superseded-by: event-log-split-into-two
  summary: |
    The two-tables-under-one-noun overlap is resolved by splitting
    event-log into an audit-log-only concept and folding the named-event
    ledger material into concepts/named-event.md. See event-log-split-into-two
    in _resolved/ for the picked shape and outcome.
---

# Two distinct tables (`rimsky_events`, `rimsky_node_events`) share the noun "events" but serve different consumers

## What is muddy

The word "events" covers two independent storage surfaces:

- **`rimsky_events`** — rimsky-internal audit log. Rimsky writes; rimsky reads (for the `/events` dashboard route).
- **`rimsky_node_events`** — executor-emitted `NamedEvent` ledger. Executor writes; rimsky reads only at the sanctioned substitution leaf and persistence-fetch sites.

Different shapes, different opacity disciplines, different read patterns, different consumers. They share only the word "events" and the `occurred_at` column.

## Why it matters

A casual reader mistakes one for the other. A new feature touching "events" needs to be explicit about which surface. The dashboard's `/events` route is rimsky-events; the substitution `{{nodes.<emitter>.event.<name>.<path>}}` is node-events.

## Resolution candidates (do NOT pick)

- Rename one (e.g., `rimsky_audit` and `rimsky_node_events`).
- Document the two tables side-by-side in `docs/concepts/operational-health.md`.
- Adopt distinct nouns ("audit log" vs "named events") in prose; reserve "events" only when the context disambiguates.

## Evidence

- `_discover/2026-05-10-event-log-append-only-jsonb.md` Observations bullet 1.

