---
trap: http-tag-create-idempotent
release: d977250c
demonstration: experiment:assumption-http-tag-create-idempotent
---
## Assumption

As operator re-running a deployment script, I would take it that creating a tag or registering a template that already exists returns success rather than a conflict, the same way re-creating an instance under an existing key does.

sibling-symmetry — `concept:instance` states create-by-key is idempotent and `concept:template` states re-registration is idempotent

## Actual behavior

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
