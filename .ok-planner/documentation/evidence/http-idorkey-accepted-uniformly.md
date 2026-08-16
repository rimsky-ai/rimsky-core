---
trap: http-idorkey-accepted-uniformly
release: d977250c
---
# Evidence set — any route taking an instance identifier accepts either the UUID or the instance key, since several are spelled `{idOrKey}`.

Source of the prior: sibling-symmetry — `{idOrKey}` on lifecycle routes versus bare `{id}` on the asset, frame, message, and debug routes

## What the audit ran and observed (assumption record)

Ran `experiments/assumption-http-idorkey-accepted-uniformly` (21 checks, pass)
against one `rimsky-all-in-one` container at this tree, calling every
instance-scoped route twice — once by UUID, once by the instance key.

The prior is contradicted, and the split is exactly the route spelling. Seven
`{idOrKey}` routes take the key and answer identically either way:
`GET /v1/instances/{k}`, `/nodes`, `/breakpoints`, `/breakpoint-hits`, and
`POST .../pause`, `/resume`, `/breakpoints`; `POST .../terminate` and
`DELETE /v1/instances/{k}` take it too. Ten `{id}` routes reject the key with 400
`"invalid instance id"` — `GET /frames`, `GET /frames/{frame_id}`,
`GET /messages`, `POST /messages`, `GET /assets`, `GET /assets/{alias}`,
`GET /assets/{alias}/versions`,
`GET /assets/{alias}/materialization-history`, `DELETE /assets/{alias}` and
`POST /debug/override` — while the same call by UUID succeeds.

An operator who names instances by key can create, inspect, pause, resume,
terminate and delete them by that name, but cannot read their frames, messages or
assets, or issue a debug override, without first resolving the key to a UUID
through `GET /v1/instances/{key}`. Nothing in the response says so: the error is
`invalid instance id`, which reads as "you sent a malformed id", not "this route
does not take keys".

The two spellings also disagree about an unknown name.
`GET /v1/instances/never-created` answers 404 `instance not found`;
`GET /v1/instances/never-created/frames` answers 400 `invalid instance id`. Same
prefix, same name, two different verdicts about what went wrong.

## Experiment record (experiment:assumption-http-idorkey-accepted-uniformly)

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

Runnables: `src:.ok-planner/experiments/assumption-http-idorkey-accepted-uniformly/` at the stamped commit.
