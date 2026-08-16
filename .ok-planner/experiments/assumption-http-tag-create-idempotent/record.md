---
experiment: assumption-http-tag-create-idempotent
commit: PENDING
---

# Is creating a tag idempotent, the way its siblings are?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It POSTs each
creating route twice with an identical body — template register, template deploy,
instance create by key, tag create — compares the second status, then re-runs the
same pair through the `rimsky template register` and `rimsky tag create` CLI
verbs a deployment script would call, and finally replays the whole deployment.

## What was observed

The three siblings the prior reasons from are idempotent. `POST /v1/templates`
answers 201 then 200 with the same `template_id`. `POST /v1/templates/{id}/deploy`
answers 200 then 200 with `no_op: true`. `POST /v1/instances` under one
`instance_key` answers 201 then 200 with the same `instance_id`.

`POST /v1/tags` answers 201 then **409** `{"error": "tag already exists"}`, and a
third identical POST is 409 again. `PUT /v1/tags/{tag}` is the idempotent form —
200 then 200 with the same body — but it is not a create: `PUT` on a tag that does
not exist answers 404, so neither verb alone covers create-or-update.

The CLI carries the split through unchanged. `rimsky template register` run twice
exits 0 both times. `rimsky tag create` run twice exits 0 then **1**, printing
`control-api POST …/v1/tags: 409 tag already exists`.

Replaying the four-call deployment end to end, the only call that fails is
`/v1/tags`.

EXPERIMENT PASS (10 checks)
