---
experiment: assumption-http-status-codes-conventional
commit: PENDING
---

# Conventional 401 / 403 / 404 / 409 on every route?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It bootstraps an
admin key and a `read-only` key through the `rimsky auth` CLI, then hits eleven
route families with no token, with an invalid token, with the under-permissioned
key, and with well-formed but unknown ids, and provokes four write conflicts.

## What was observed

The four mappings hold across the families checked. Eleven routes spanning
instances, templates, events, audit, tags, auth, observability and nodes answered
401 with no token and 401 with an invalid token, while `GET /v1/health` stayed
open. Ten write routes answered 403 for the `read-only` key, which still read
instances at 200. Eighteen routes answered 404 for a well-formed unknown id. Four
conflicting writes answered 409: a duplicate tag, undeploying a template with live
instances, deleting a still-deployed template, and deleting a non-terminal
instance.

Three departures showed up. `GET /v1/instances/{unknown uuid}/messages` answers
200 with an empty list rather than 404, as does
`GET /v1/claim-handles/{unknown uuid}/holders` — an id that names nothing reads as
an empty collection. A malformed `?cursor=` — pure client input — answers **500**
on `/v1/instances`, `/v1/templates`, `/v1/events`, `/v1/audit` and
`/v1/observability/instances`, leaking the internal message
`instances.list: bad cursor: illegal base64 data at input byte 0`. And addressing
an instance by its key on an `{id}`-spelled route answers 400
`"invalid instance id"` where the sibling `{idOrKey}` route answers 404.

Outside JSON entirely: an unmatched `/v1` path answers a plain-text 404, and a
wrong method answers 405 with an empty body.

EXPERIMENT PASS (57 checks)
