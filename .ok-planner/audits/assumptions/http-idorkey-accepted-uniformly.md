---
assumption: http-idorkey-accepted-uniformly
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# any route taking an instance identifier accepts either the UUID or the instance key, since several are spelled `{idOrKey}`.

As operator addressing an instance, I would take it that any route taking an instance identifier accepts either the UUID or the instance key, since several are spelled `{idOrKey}`.

## Source

sibling-symmetry — `{idOrKey}` on lifecycle routes versus bare `{id}` on the asset, frame, message, and debug routes

## What a run would observe

call every instance-scoped route with an instance key rather than a UUID and record which reject it.

## Measured

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
