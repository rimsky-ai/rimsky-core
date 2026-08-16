---
trap: http-delete-idempotent
release: d977250c
---
# Evidence set — `DELETE` on an already-absent resource succeeds (204/200) rather than erroring, on every `DELETE` route.

Source of the prior: craft-convention — HTTP DELETE idempotence

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-http-delete-idempotent` (17 checks, pass) against one
`rimsky-all-in-one` container at this tree, deleting each deletable resource
twice and deleting four never-created resources.

No `DELETE` route on the control API is idempotent except `DELETE /v1/mcp`, which
tears down a session rather than a resource. The operator writing a teardown
script gets a first delete that succeeds and a second that answers 404 with the
resource's not-found string — on `/v1/tags/{tag}` (200 then 404),
`/v1/instances/{id}/breakpoints/{id}` (204 then 404), `/v1/instances/{id}` (200
then 404) and `/v1/templates/{id}` (200 then 404). Deleting something that never
existed answers 404 identically, including `/v1/auth/keys/{nameOrID}` and
`/v1/instances/{id}/assets/{alias}`.

So a teardown script re-run after a partial failure exits non-zero on every
resource the first run already removed, and the operator cannot distinguish "this
was already gone" from "this delete failed" without parsing the message string —
which, per the envelope, is prose with no code.

The success code is not uniform either: 200 for a tag, an instance and a
template, 204 for a breakpoint. A resource that is still live refuses with 409
and keeps refusing (`instance is not in terminal state`,
`template is in 'deployed' state`), so the teardown order is forced: terminate,
delete instances, undeploy, delete template.

## Experiment record (experiment:assumption-http-delete-idempotent)

# Is DELETE idempotent on every DELETE route?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It creates a
template, a tag, two instances and a breakpoint through the control API, deletes
each deletable resource twice, and records the second response. It also deletes
four never-created resources and walks the teardown order a script would actually
write (terminate, delete instances, undeploy, delete template).

## What was observed

No `DELETE` route on the control API is idempotent except `DELETE /v1/mcp`.

The first delete succeeds and the second answers 404 with the resource's
not-found string, on every route tried: `/v1/tags/{tag}` (200 then 404),
`/v1/instances/{id}/breakpoints/{id}` (204 then 404), `/v1/instances/{id}` (200
then 404) and `/v1/templates/{id}` (200 then 404). The success code is not uniform
either — 200 for a tag, an instance and a template; 204 for a breakpoint.

Deleting a resource that never existed answers 404 the same way, on
`/v1/tags/{tag}`, `/v1/templates/{id}`, `/v1/instances/{id}`,
`/v1/auth/keys/{nameOrID}` and `/v1/instances/{id}/assets/{alias}`; a malformed
asset alias answers 400 instead.

`DELETE /v1/mcp` answers 200 on both calls — the one idempotent delete, and it
tears down a session rather than a resource.

Deleting a live resource answers 409 and repeats itself: a non-terminal instance
answers 409 on every call until it is terminated, and a deployed template answers
409 until it is undeployed.

EXPERIMENT PASS (17 checks)

Runnables: `src:.ok-planner/experiments/assumption-http-delete-idempotent/` at the stamped commit.
