---
experiment: assumption-http-events-streamable
commit: PENDING
---

# Does `GET /v1/events` stream?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It requests
`/v1/events` and `/v1/observability/events` with `Accept: text/event-stream` and
with five plausible follow parameters, reads the response headers, then runs
`rimsky logs --follow` against a live instance and counts the requests that
arrive, reading them out of the deployment's own audit log.

## What was observed

Neither events route streams. With `Accept: text/event-stream` both answer 200
with `Content-Type: application/json` and a `Content-Length` matching the body
exactly — a complete, finite response, connection closed. `?follow=true`,
`?follow=1`, `?stream=true`, `?watch=true` and `?tail=true` are all accepted and
ignored: each answers 200 with the same `{events, next_cursor}` envelope and the
same row count as the parameterless call. `?since=` is a filter, not a resume
offset — it demands an RFC3339 timestamp and rejects `0` with 400.

No route on the surface holds a connection open.
`GET /v1/instances/{id}/breakpoint-hits`, the one route shaped like a long-poll,
returns immediately with `{"hits": [], "next_since": 0, "truncated": false}`.

The CLI's follow is client-side polling and says so. `rimsky watch` and
`rimsky instance events` both advertise `-poll-interval` in their own help, the
latter documented as "polling interval when --follow", default 1s. Running
`rimsky logs <id> --follow --poll-interval 200ms` and counting requests in the
audit log, one `--follow` run issued five separate `GET /v1/events` requests
rather than holding one stream.

EXPERIMENT PASS (14 checks)
