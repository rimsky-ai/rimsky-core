---
audit: event-log-read
artifact: story:event-log-read
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:34Z
---

# Operator reads unified chronological event feed

Supported. `GET /v1/events` reads from the single `rimsky_events` table
shared by every event kind — node lifecycle (`state_transition`), breakpoint
hits (`breakpoint.hit`, emitted by the breakpoint evaluator into the same
table), message activity (`message_sent`/`message_received`), and
supervisor decisions (`work_started`/`work_completed`/`work_rejected`,
`operator_override`) all land in the one log with no per-kind partitioning.
Both the Postgres and SQLite implementations order every read
`ORDER BY occurred_at DESC, id DESC` over the whole table regardless of
kind, so cross-kind chronological ordering is a structural property of the
single query, not something assembled from separate streams. The route
supports `kind` (validated against the full operational-kind vocabulary
plus signal type-paths) and `since`/`until` filters, both covered by
route-level tests, along with instance/node scoping and cursor pagination
walking every seeded row without loss or duplication.
