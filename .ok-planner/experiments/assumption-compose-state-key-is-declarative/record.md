---
experiment: assumption-compose-state-key-is-declarative
commit: d977250c
---

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
