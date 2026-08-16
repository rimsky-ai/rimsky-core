---
trap: http-status-codes-conventional
release: d977250c
demonstration: experiment:assumption-http-status-codes-conventional
---
## Assumption

As client-library author, I would take it that missing token yields 401, insufficient permission yields 403, unknown id yields 404, and a conflicting write yields 409 — uniformly across all routes.

ecosystem-prior — REST APIs of this shape

## Actual behavior

Ran `experiments/assumption-http-status-codes-conventional` (57 checks, pass)
against one `rimsky-all-in-one` container at this tree, hitting eleven route
families with no token, an invalid token, a `read-only` key and well-formed
unknown ids, and provoking four write conflicts.

The four mappings mostly hold: eleven routes answered 401 with no token and with
an invalid token, ten write routes answered 403 for the `read-only` key, eighteen
routes answered 404 for an unknown well-formed id, and four conflicting writes
answered 409. The prior fails on "uniformly across all routes".

Unknown id does not always read 404. `GET /v1/instances/{unknown uuid}/messages`
answers 200 with an empty list, as does
`GET /v1/claim-handles/{unknown uuid}/holders` — an id naming nothing reads as an
empty collection, so a client cannot tell "no messages" from "no such instance".

Worse for the client-library author the prior speaks for: a malformed `?cursor=`
is client input and answers **500**, on `/v1/instances`, `/v1/templates`,
`/v1/events`, `/v1/audit` and `/v1/observability/instances`, with the internal
message `instances.list: bad cursor: illegal base64 data at input byte 0` in the
body. Retry-on-5xx logic will loop forever on a request that can never succeed.

Two more: addressing an instance by its key on an `{id}`-spelled route answers 400
where the sibling `{idOrKey}` route answers 404, and both an unmatched path (404,
`text/plain`) and a wrong method (405, empty body) leave the JSON surface.
