---
experiment: mandatory-instantiation-gate
commit: PENDING
---

# Create-time validation of static attribute config

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image, with the
in-process `verifier-shape-checks` and `verifier-http` executors. Each
template supplies its attribute config only through
`defaults.attributes.by_executor`, which registration's
composition-against-executor check does not see, so the create-time gate is
the one thing that can catch it. Creates go through the public control-api
route so the refusal body is visible. `run.sh` boots and removes the
container.

## What was observed

A template whose `checks` default is an empty array registered and deployed,
and instance create was refused with HTTP 400: the body named the node
(`nodes[shape].attributes`), the attribute (`/checks`) and the violated
constraint (`minItems: minimum 1 items required, but found 0 items`) — a value
constraint, not a shape mismatch. No instance was created. A two-executor
template whose second service's `url` default is a number was likewise refused,
naming `nodes[fetch].attributes`, `/url` and `expected string`. A template
whose config satisfies both services' schemas created cleanly with both nodes.

The CLI relays only the summary line of the refusal — `400 template validation
failed` — and drops the `validation_errors` detail that the control-api
response carries.
