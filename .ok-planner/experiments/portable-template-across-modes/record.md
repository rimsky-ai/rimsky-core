---
experiment: portable-template-across-modes
commit: d977250c
---

# One template file, run unedited in both deployment modes

## What it ran against

`way-both-modes.py` runs `template.yml` — one file committed beside it, naming
the bundled `verifier-shape-checks` executor and carrying its own inline dataset
— against two deployments, using the CLI binary built from this tree. The first
is a `rimsky-all-in-one` container with its baked zero-config SQLite defaults.
The second is a multi-container deployment on its own docker network: a postgres
container, a `rimsky-executor-verifier-shape-checks` container, and three
`rimsky` containers whose commands name `rimsky-control-api`, `rimsky-scheduler`
and `rimsky-supervisor` against a shared postgres config. The file is hashed
before and after.

## What was observed

The same file registered, deployed and instantiated on both deployments, and the
run verb printed an instance id in each. The watch verb returned 0 in each, and
every node reported one fresh run on each. The file's hash was unchanged after
both runs, and both deployments content-addressed it to the same template hash,
`sha256-9b644a3f…`.

Nine checks, none failing.

RESULT: PASS
