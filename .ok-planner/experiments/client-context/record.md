---
experiment: client-context
commit: PENDING
---

# Two deployments registered, switched between, inspected, and removed

## What it ran against

Two independent `rimsky-all-in-one` containers from this tree's image set, each
seeded with a distinct template while still addressed explicitly. After the two
`ctx add` calls no command names an endpoint.

## What was observed

`ctx add alpha` and `ctx add beta` registered both endpoints; `ctx list` showed
both names with their endpoints and marked the current one; `ctx current`
reported `alpha`, the first added. With no endpoint flag anywhere, `ls templates`
against `alpha` returned alpha's template hash and not beta's. `ctx use beta`
switched the current context, `ctx current` confirmed it, and the same flagless
`ls templates` then returned beta's hash and not alpha's — so the switch really
re-targets the deployment rather than only rewriting a file. `ctx rm alpha`
removed the non-current entry and left `beta` listed.

RESULT: PASS
