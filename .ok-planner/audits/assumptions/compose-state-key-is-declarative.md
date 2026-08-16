---
assumption: compose-state-key-is-declarative
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `templates[].state` makes the manifest declarative — setting a template to undeployed on the next `compose up` undeploys it, and removing an entry removes the resource.

As operator managing a project, I would take it that `templates[].state` makes the manifest declarative — setting a template to undeployed on the next `compose up` undeploys it, and removing an entry removes the resource.

## Source

name-promise — a `state` field in a manifest with `up`/`down`/`plan` verbs

## What a run would observe

flip `templates[].state` in a manifest, re-run `compose up`, and check whether the live template's deployment state follows.

## Measured

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
