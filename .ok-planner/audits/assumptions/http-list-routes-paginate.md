---
assumption: http-list-routes-paginate
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every collection route (`GET /v1/instances`, `/v1/templates`, `/v1/events`, `/v1/audit`, observability collections) accepts the same pagination params and returns the same page envelope with a next cursor.

As operator with a large deployment, I would take it that every collection route (`GET /v1/instances`, `/v1/templates`, `/v1/events`, `/v1/audit`, observability collections) accepts the same pagination params and returns the same page envelope with a next cursor.

## Source

craft-convention — collection endpoints in a production control plane

## What a run would observe

request each collection route with a small page size and follow the cursor, checking the parameter names and envelope are the same everywhere.

## Measured

Ran `experiments/assumption-http-list-routes-paginate` (36 checks, pass) against
one `rimsky-all-in-one` container at this tree seeded with three templates, three
tags and three instances, following `next_cursor` to the end on every collection
route.

The prior holds for the core of the surface and fails at its edges. Eight
collections share the contract exactly — `/v1/instances`, `/v1/templates`,
`/v1/events`, `/v1/audit`, `/v1/tags`, `/v1/observability/instances`,
`/v1/observability/events`, `/v1/observability/templates` — all taking `limit`
and `cursor`, returning `{<items>, next_cursor}`, and walking to the end with one
loop.

Four departures break the one paging helper the operator would write.
`/v1/observability/executors` and `/v1/observability/claim-producers` carry no
`next_cursor` at all and ignore `?limit=` entirely. Every instance-nested
collection except `/nodes` differs: `/frames` and `/messages` omit `next_cursor`
on the last page rather than sending `""` (so `body["next_cursor"]` raises),
`/assets`, `/breakpoints` and `/v1/claim-handles/{id}/holders` have no cursor
field at all, and `/breakpoint-hits` pages on `since` / `next_since` /
`truncated` instead. An empty page is `[]` on the core routes and `null` on the
observability routes.

The cursor itself is not one token type — a base64 blob on the core routes, the
raw tag value on `/v1/tags` — which is why a malformed cursor answers 500
everywhere but `/v1/tags`, where it answers 200 and silently pages from it. A
malformed `?limit=` diverges the same way: 200 and a default on `/v1/instances`,
400 on `/v1/observability/events`.
