---
experiment: assumption-http-list-routes-paginate
commit: d977250c
---

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
