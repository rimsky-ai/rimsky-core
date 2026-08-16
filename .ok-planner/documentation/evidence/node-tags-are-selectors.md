---
trap: node-tags-are-selectors
release: d977250c
---
# Evidence set — `nodes[].tags` are operator-facing labels usable for selection, so `--tag` and `--tag-prefix` filter nodes and runs by them.

Source of the prior: name-promise — `nodes[].tags` in the template beside `--tag` and `--tag-prefix` CLI flags

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-node-tags-are-selectors)

# Whether node tags select anything

## What it ran against

One `rimsky-all-in-one` container from this tree's image set, carrying a
template whose two nodes hold different `nodes[].tags` — `alpha` tagged
`team-a` and `critical`, `beta` tagged `team-b` — and one instance of it.
Three questions: does any surface select nodes by those tags; do the CLI's
`--tag` and `--tag-prefix` flags bind to them; does the CLI show them at all.

## What was observed

The tags are a real selector, on one surface: `GET
/v1/instances/{id}/nodes?tag=team-a` returned exactly `alpha`, with its tag
list intact.

Nothing in the CLI reaches it. `--tag` and `--tag-prefix` are undefined on
`instance nodes`, `instance list`, and `node get`. Where the two flags do
exist they name a different noun: `--tag` on `template register` attaches a
template tag, and `--tag-prefix` on `template list` filters templates by
theirs. An operator following the flag names filters templates while believing
they filtered nodes.

The CLI's node listing also drops the tags: `rimsky instance nodes -o json`
returned `[["alpha", null], ["beta", null], ["", null]]` against a deployment
whose HTTP route returns `["team-a", "critical"]` for the same node. 3 checks,
1 pass, 2 fail.

Runnables: `src:.ok-planner/experiments/assumption-node-tags-are-selectors/` at the stamped commit.
