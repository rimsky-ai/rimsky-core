---
experiment: assumption-http-idorkey-accepted-uniformly
commit: d977250c
---

# Does every instance-scoped route take the instance key?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. It creates one
instance with a known `instance_key`, then calls every instance-scoped route twice
— once with the UUID, once with the key — and compares the two answers.

## What was observed

The split follows the route spelling exactly, and it is not a distinction a user
can see from the outside.

Seven routes spelled `{idOrKey}` take the key and answer identically either way:
`GET /v1/instances/{k}`, `/nodes`, `/breakpoints`, `/breakpoint-hits`, and
`POST .../pause`, `/resume`, `/breakpoints`. The teardown pair
`POST .../terminate` and `DELETE /v1/instances/{k}` take it too.

Ten routes spelled `{id}` reject the key with 400 `"invalid instance id"` —
`GET /frames`, `GET /frames/{frame_id}`, `GET /messages`, `POST /messages`,
`GET /assets`, `GET /assets/{alias}`, `GET /assets/{alias}/versions`,
`GET /assets/{alias}/materialization-history`, `DELETE /assets/{alias}` and
`POST /debug/override` — while the same call by UUID succeeds.

The two spellings also disagree about what an unknown name means: for a name that
was never an instance, `GET /v1/instances/never-created` answers 404
`"instance not found"` and `GET /v1/instances/never-created/frames` answers 400
`"invalid instance id"`. Same path prefix, same unknown name, two different
verdicts about what went wrong.

EXPERIMENT PASS (21 checks)
