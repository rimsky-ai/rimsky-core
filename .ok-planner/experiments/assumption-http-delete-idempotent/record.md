---
experiment: assumption-http-delete-idempotent
commit: PENDING
---

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
