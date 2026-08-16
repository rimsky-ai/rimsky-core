---
trap: http-events-streamable
release: d977250c
demonstration: experiment:assumption-http-events-streamable
---
## Assumption

As operator watching a live run, I would take it that `GET /v1/events` supports a streaming/follow mode (SSE or long-poll), because the CLI offers `rimsky watch` and a `--follow` flag.

name-promise — `rimsky watch`, `--follow`, `rimsky logs` over a thin HTTP client

## Actual behavior

Ran `experiments/assumption-http-events-streamable` (14 checks, pass) against one
`rimsky-all-in-one` container at this tree, requesting the events routes with
`Accept: text/event-stream` and five follow parameters, and then running
`rimsky logs --follow` and counting the requests it made in the deployment's own
audit log.

`GET /v1/events` has no streaming or follow mode. With
`Accept: text/event-stream` it answers 200 `Content-Type: application/json` with a
`Content-Length` matching the body — a complete finite response — and so does
`/v1/observability/events`. `?follow=true`, `?follow=1`, `?stream=true`,
`?watch=true` and `?tail=true` are all accepted and silently ignored, each
returning the same `{events, next_cursor}` envelope and row count as the
parameterless call, so a client that adds a follow parameter gets no error and no
stream. `?since=` is a filter taking an RFC3339 timestamp, not a resume offset.
No route holds a connection open: `/v1/instances/{id}/breakpoint-hits`, the one
long-poll-shaped route, returns immediately.

The name-promise the prior read is real but means the opposite of what it looks
like. `rimsky watch` and `rimsky instance events` both advertise
`-poll-interval` in their own help, the latter as "polling interval when
--follow", default 1s. One `rimsky logs --follow --poll-interval 200ms` run
issued five separate `GET /v1/events` requests. The follow is client-side
polling; an operator writing their own thin HTTP client and looking for the
server's stream will not find one, and a large deployment pays a full re-query
per interval per watcher.
