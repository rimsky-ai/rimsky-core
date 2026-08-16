---
trap: http-list-routes-paginate
release: d977250c
---
# Evidence set — every collection route (`GET /v1/instances`, `/v1/templates`, `/v1/events`, `/v1/audit`, observability collections) accepts the same pagination params and returns the same page envelope with a next cursor.

Source of the prior: craft-convention — collection endpoints in a production control plane

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-http-list-routes-paginate)

# One pagination contract across every collection route?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, seeded with three
templates, three tags and three instances. It requests each collection route with
`?limit=1`, follows `next_cursor` to the end, and compares parameter names,
envelope keys, empty-page representation and malformed-input handling across the
top-level collections, the observability collections and the instance-nested
collections.

## What was observed

Eight collections share the contract exactly — `/v1/instances`, `/v1/templates`,
`/v1/events`, `/v1/audit`, `/v1/tags`, `/v1/observability/instances`,
`/v1/observability/events` and `/v1/observability/templates` all take `limit` and
`cursor`, return `{<items>, next_cursor}`, and walk to the end with one loop. The
five settled ones each yielded three distinct rows; the three event-log ones
returned descending ids, newest first.

Four departures. `/v1/observability/executors` and
`/v1/observability/claim-producers` carry no `next_cursor` key at all and ignore
`?limit=` entirely — `?limit=1` returned all three executors. Every
instance-nested collection except `/nodes` breaks the envelope a different way:
`/frames` and `/messages` omit `next_cursor` when there is no next page rather
than sending `""`, `/assets`, `/breakpoints` and `/v1/claim-handles/{id}/holders`
have no cursor field at all, and `/breakpoint-hits` pages on an entirely
different triple, `since` / `next_since` / `truncated`. An empty page is `[]` on
the core routes and `null` on the observability routes.

The cursor is not one opaque token either: the core routes hand back a base64
blob while `/v1/tags` hands back the raw tag value. Consequently a malformed
cursor answers 500 on `/v1/instances`, `/v1/templates`, `/v1/events`, `/v1/audit`
and `/v1/observability/instances`, but 200 on `/v1/tags`, which silently pages
from it. A malformed `?limit=` diverges the same way — `/v1/instances` falls back
to its default and answers 200, `/v1/observability/events` rejects it with 400.

EXPERIMENT PASS (36 checks)

Runnables: `src:.ok-planner/experiments/assumption-http-list-routes-paginate/` at the stamped commit.
