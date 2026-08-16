---
trap: http-tag-create-idempotent
release: d977250c
---
# Evidence set — creating a tag or registering a template that already exists returns success rather than a conflict, the same way re-creating an instance under an existing key does.

Source of the prior: sibling-symmetry — `concept:instance` states create-by-key is idempotent and `concept:template` states re-registration is idempotent

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-http-tag-create-idempotent` (10 checks, pass) against
one `rimsky-all-in-one` container at this tree, POSTing each creating route twice
with an identical body and re-running the same pair through the CLI verbs.

The prior is half right, and the half it gets wrong is the tag. The two siblings
it reasons from are idempotent exactly as `concept:instance` and
`concept:template` say: `POST /v1/templates` answers 201 then 200 with the same
`template_id`, `POST /v1/instances` under one `instance_key` answers 201 then 200
with the same `instance_id`, and `POST /v1/templates/{id}/deploy` answers 200 then
200 with `no_op: true`.

`POST /v1/tags` answers 201 then **409** `{"error": "tag already exists"}`, and
stays 409 on every repeat. The idempotent form exists but is a different verb:
`PUT /v1/tags/{tag}` answers 200 twice — and is not a create, since `PUT` on an
absent tag answers 404. So neither verb alone is create-or-update, and the caller
has to know which to use before they can write a re-runnable script.

The CLI carries the split straight through to the operator: `rimsky template
register` run twice exits 0 both times, `rimsky tag create` run twice exits 0
then **1**. Replaying a four-call deployment end to end, the tag is the only call
that fails — so a re-run script dies on exactly the one step the operator had
every reason to expect would pass.

## Experiment record (experiment:assumption-http-tag-create-idempotent)

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

Runnables: `src:.ok-planner/experiments/assumption-http-tag-create-idempotent/` at the stamped commit.
