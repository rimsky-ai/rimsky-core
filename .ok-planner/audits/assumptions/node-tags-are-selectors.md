---
assumption: node-tags-are-selectors
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `nodes[].tags` are operator-facing labels usable for selection, so `--tag` and `--tag-prefix` filter nodes and runs by them.

As operator filtering by label, I would take it that `nodes[].tags` are operator-facing labels usable for selection, so `--tag` and `--tag-prefix` filter nodes and runs by them.

## Source

name-promise — `nodes[].tags` in the template beside `--tag` and `--tag-prefix` CLI flags

## What a run would observe

tag two nodes, then filter `rimsky instance nodes --tag <value>` and see whether the flag binds to node tags or to template tags.

## Measured

`.ok-planner/experiments/assumption-node-tags-are-selectors` — built for this
run — registered a template whose two nodes carry different `nodes[].tags`,
created an instance, and asked one `rimsky-all-in-one` from this tree's image
set whether anything selects by them.

Something does, over HTTP: `GET /v1/instances/{id}/nodes?tag=team-a` returned
exactly the node tagged `team-a`, tags included. So the tags are real
selectors and the prior's premise is sound.

The flags it names are not the way in. `--tag` and `--tag-prefix` are
undefined on `instance nodes`, `instance list`, and `node get` — every verb
that lists nodes — and where they do exist they select a different noun
entirely: `--tag` on `template register` attaches a template tag, and
`--tag-prefix` on `template list` filters templates by theirs. An operator who
tags nodes and then reaches for `--tag-prefix` filters templates and gets a
plausible, empty answer rather than an error.

The CLI does not even show the tags it cannot filter on: `rimsky instance
nodes -o json` returned `[["alpha", null], ["beta", null], ["", null]]` for
the same nodes whose HTTP representation carries `["team-a", "critical"]`. So
from the CLI the tags are invisible as well as unselectable, and the operator
has no way to notice. 3 checks, 1 pass, 2 fail.
