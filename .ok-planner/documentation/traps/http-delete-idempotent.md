---
trap: http-delete-idempotent
release: d977250c
demonstration: experiment:assumption-http-delete-idempotent
---
## Assumption

As operator writing a teardown script, I would take it that `DELETE` on an already-absent resource succeeds (204/200) rather than erroring, on every `DELETE` route.

craft-convention — HTTP DELETE idempotence

## Actual behavior

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
