---
trap: http-observability-mirrors-primary
release: d977250c
---
# Evidence set — each `/v1/observability/<thing>` read returns the same resource shape as the corresponding primary route, just under a read-only permission, so a dashboard can read either.

Source of the prior: sibling-symmetry — `/v1/instances/{id}` beside `/v1/observability/instances/{id}`, likewise nodes, events, frames

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-http-observability-mirrors-primary)

# Does `/v1/observability/<thing>` mirror the primary route?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, seeded with a
template and an instance. It fetches the same resource through both routes with
an admin key and diffs the JSON — instance single-get, instance list, template
list, events, nodes, frames — then mints a key granted only `instance:read` and
tries both halves with it.

## What was observed

The single-instance read is not the same resource. `GET /v1/instances/{id}`
returns the instance object at the top level. `GET /v1/observability/instances/{id}`
returns `{"instance": …, "cascade_graph": […]}`, so a dashboard reading
`body["id"]` gets the id from one route and nothing from the other.

The mirror carries fields the primary route hides. The wrapped instance adds
`attribute_overrides` and `terminated_at`; nothing on the primary object is
missing from it. `terminated_at` is the field `DELETE /v1/instances/{id}` names in
its own 409 — "wait for terminated_at to be set" — and it is not on the resource
that route's sibling GET returns.

The list routes agree on the envelope and disagree on the row: both instance
lists use `{instances, next_cursor}`, but the mirror's rows carry
`attribute_overrides` and `terminated_at`, and the mirror's template rows carry
`spec`. Events are the one true pair — same envelope, same row keys either way.

Nodes and frames are not addressed the same way at all. `GET /v1/nodes/{node_id}`
takes a node id; the mirror is `GET /v1/observability/nodes/{instance_id}/{node_type}`
and returns `events`, `holdings`, `latest_attributes`, `node` and `run_summary`.
There is no by-node-id read on the mirror — that path 404s. The primary frames
read is instance-scoped with no cursor; the mirror is a filtered global collection
with one.

It is a different permission, not a read-only variant. A key granted only
`instance:read` reads `/v1/instances` at 200 and is refused 403 on
`/v1/observability/instances`, on the single-instance mirror, and on
`/v1/observability/system/health`.

The two halves do not report failure the same way either: on a 404 the primary
sends `error` as a string and the mirror sends it as `{"code", "message"}`.

EXPERIMENT PASS (18 checks)

Runnables: `src:.ok-planner/experiments/assumption-http-observability-mirrors-primary/` at the stamped commit.
