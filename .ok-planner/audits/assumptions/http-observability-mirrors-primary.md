---
assumption: http-observability-mirrors-primary
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# each `/v1/observability/<thing>` read returns the same resource shape as the corresponding primary route, just under a read-only permission, so a dashboard can read either.

As dashboard author, I would take it that each `/v1/observability/<thing>` read returns the same resource shape as the corresponding primary route, just under a read-only permission, so a dashboard can read either.

## Source

sibling-symmetry — `/v1/instances/{id}` beside `/v1/observability/instances/{id}`, likewise nodes, events, frames

## What a run would observe

fetch the same instance through both routes with an admin key and diff the JSON.

## Measured

Ran `experiments/assumption-http-observability-mirrors-primary` (18 checks, pass)
against one `rimsky-all-in-one` container at this tree, fetching the same
resources through both halves with an admin key and diffing, then trying both
with a key granted only `instance:read`.

The observability family is a second, differently-shaped read surface, not a
read-only mirror. `GET /v1/instances/{id}` returns the instance object at the top
level; `GET /v1/observability/instances/{id}` returns
`{"instance": …, "cascade_graph": […]}`, so a dashboard reading `body["id"]` gets
the id from one and `None` from the other. Nodes are not even addressed the same
way: `GET /v1/nodes/{node_id}` takes a node id, while the mirror is
`GET /v1/observability/nodes/{instance_id}/{node_type}` returning `events`,
`holdings`, `latest_attributes`, `node` and `run_summary` — and there is no
by-node-id read on the mirror at all. Frames diverge the same way: instance-scoped
without a cursor on one side, a filtered global collection with one on the other.

The mirror is also not the same data. Its instance rows carry
`attribute_overrides` and `terminated_at`, and its template rows carry `spec`,
none of which the primary routes return. `terminated_at` is the field
`DELETE /v1/instances/{id}` names in its own 409 — "wait for terminated_at to be
set" — and the primary instance GET does not return it, so the route that tells
you what to wait for and the route you would poll are on opposite sides of the
line.

It is a different permission, not a read-only posture: a key granted only
`instance:read` reads `/v1/instances` at 200 and is refused 403 on
`/v1/observability/instances`. And the two halves report failure differently — on
a 404 the primary sends `error` as a string, the mirror as `{"code", "message"}`.

Events are the one genuine pair, same envelope and same row keys either way,
which is what makes the rest of the family read as a mirror when it is not.
