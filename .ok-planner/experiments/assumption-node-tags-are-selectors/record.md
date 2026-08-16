---
experiment: assumption-node-tags-are-selectors
commit: PENDING
---

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
