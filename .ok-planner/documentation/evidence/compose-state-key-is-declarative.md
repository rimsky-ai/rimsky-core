---
trap: compose-state-key-is-declarative
release: d977250c
---
# Evidence set — `templates[].state` makes the manifest declarative — setting a template to undeployed on the next `compose up` undeploys it, and removing an entry removes the resource.

Source of the prior: name-promise — a `state` field in a manifest with `up`/`down`/`plan` verbs

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-compose-state-key-is-declarative` — built
for this run — brought a compose project up with `state: deployed` against one
`rimsky-all-in-one` from this tree's image set, then flipped the key and
removed entries, reading the live world back after each `compose up`.

The first clause is contradicted twice over. `state: undeployed` is not a
value the manifest accepts at all: `compose plan` and `compose up` both refuse
the file with `templates[0].state: "undeployed" must be one of
registered|deployed`, so the operator's manifest stops working rather than
undeploying anything. The value that *is* legal, `state: registered`, parses
and does nothing — `compose up` reported `no changes` and the template stayed
`deployed`. The key only moves a template forward; there is no way to walk one
back through the manifest.

The second clause holds, with a precondition worth knowing. Dropping the
template entry undeployed, untagged, and deleted the template in one apply.
But dropping an instance entry while the instance is still live makes the
whole run refuse — `compose-owned non-terminal instances not in manifest:
compose:state-key:one (wait for terminal state and re-run, or invalidate
manually)` — and that refusal blocks every other change in the manifest, not
just the instance's own. Once terminated, the next `compose up` deleted it.
4 checks, 1 pass, 3 fail.

## Experiment record (experiment:assumption-compose-state-key-is-declarative)

# How declarative `templates[].state` and manifest removal are

## What it ran against

One `rimsky-all-in-one` container from this tree's image set and one compose
project brought up with `state: deployed`. The run then flips the key to each
value an operator might write — `undeployed`, then `registered` — re-running
`compose up` and reading the live template's state back each time. It then
drops the instance entry while the instance is still live, drops it again once
the instance is terminal, and finally drops the template entry.

## What was observed

`state: undeployed` is not a value the manifest accepts. Both `compose plan`
and `compose up` refuse the file outright: `templates[0].state: "undeployed"
must be one of registered|deployed`, and the live template stays `deployed`.

`state: registered` parses and does nothing. `compose up` reported `no
changes` and the template remained `deployed`, so the key only ever moves a
template forward — there is no rollback through it.

Removal is declarative, with a precondition. Dropping the instance entry while
the instance was still live made the whole run refuse:
`compose-owned non-terminal instances not in manifest: compose:state-key:one
(wait for terminal state and re-run, or invalidate manually)` — so one live
instance blocks every other change in the manifest, not just its own. Once the
instance was terminated, the next `compose up` deleted it, and dropping the
template entry undeployed, untagged, and deleted the template in one apply.
4 checks, 1 pass, 3 fail.

Runnables: `src:.ok-planner/experiments/assumption-compose-state-key-is-declarative/` at the stamped commit.
