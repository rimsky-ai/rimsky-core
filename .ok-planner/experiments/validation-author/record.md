---
experiment: validation-author
commit: PENDING
---

# A service's validation mix-in consulted at template registration

## What it ran against

`run.py` boots the shipped `rimsky-executor-verifier-shape-checks` image at
`RIMSKY_IMAGE_TAG` as a standalone service on a free host port. That service is
a worked example of the story's role: it serves the executor protocol, serves
the validation protocol's single `Validate` RPC, and advertises
`validation_supported_roles: ["executor"]` in its executor-observability
capabilities handshake. `run.py` then runs `rimsky conformance validation`
against it, and boots a `rimsky-all-in-one` container from the same tree with a
mounted `rimsky.yml` declaring the service under `executors:` with
`protocols: [executor, validation]`, reachable at `host.docker.internal`.
Templates go in through `POST /v1/templates`. `run.py` requires the CLI built at
`bin/rimsky` and removes both containers.

## What was observed

Five legs, nine checks, none failing.

`rimsky conformance validation --role executor` exited 0 against the service,
passing all four of its checks, including the unsupported-role case.

A node whose attributes declare no `checks` was refused: `POST /v1/templates`
answered `400`, and `validation_errors` carried the service's own finding —
class `missing_checks`, path `/executor/attributes.checks`. The path shows the
service was handed the executor role context for that node.

A node declaring a check kind the service does not implement registered
anyway: `POST /v1/templates` answered `201`, and `validation_warnings` carried
the service's finding with class `unknown_check_kind`. A node declaring a check
kind the service does implement registered with no findings at all.

Re-running the refused template against a rimsky that declares the same service
with `protocols: [executor]` alone registered it. The refusal came from the
service's validator, reached because the deployment declared the validation
mix-in.
