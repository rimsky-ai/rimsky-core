---
trap: cli-short-flags-single-dash
release: d977250c
---
# Evidence set — single-dash short forms work the way `-v` and `-h` do, so `-f` is `--follow`/`--force`, `-o` is `--output`, and short forms cluster.

Source of the prior: craft-convention — `-v` and `-h` present in `cli-verbs`, `--f` and `--o` listed among `cli-flags`

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-short-flags-single-dash` — built for
this run — drove the shipped CLI's parser over the four claims and compared
`ctx list -o json` with `ctx list --output json` byte for byte.

One claim holds: `-o` is a genuine short form of `--output`, and the two
render identically. The rest do not. `-f` is rejected by every verb that
carries `--follow` or `--force` — `instance events`, `messages tail`, `logs`,
`instance kill` — all four with `flag provided but not defined: -f`; the only
place `-f` is defined is the compose family, where it names the manifest path,
so the operator who reaches for `-f` either gets an error or, in compose,
something unrelated. Short forms do not cluster and take no attached value:
`-ojson`, `-yh`, and `-hy` are all parse errors, and only the Go spelling
`-o=json` works beside `-o json`. `-v` is the sharpest edge: `rimsky -v`
prints the version, but `rimsky ls templates -v` is a parse error, so the
short form the top level teaches does not survive one word to the right.
`-h` does work on both. `--yes` has no `-y`. 6 checks, 2 pass, 4 fail.

## Experiment record (experiment:assumption-cli-short-flags-single-dash)

# Single-dash short forms

## What it ran against

The CLI built from this tree, parser only — the endpoint points at a closed
port so "connection refused" means the flag was accepted, and `ctx list` runs
offline for the byte comparison. Four claims: `-o` is the short form of
`--output`; `-f` is the short form of `--follow` / `--force` on the four verbs
that define either; short forms cluster and take an attached value; `-v` and
`-h` work on a verb the way they work at the top level.

## What was observed

`-o` is a genuine short form: `ctx list -o json` and `ctx list --output json`
rendered identically. Nothing else holds. `-f` is rejected by all four verbs
carrying `--follow` or `--force` (`instance events`, `messages tail`, `logs`,
`instance kill`); it is defined only in the compose family, where it names the
manifest path. Clustering and attached values are rejected — `-ojson`, `-yh`,
and `-hy` all fail with "flag provided but not defined"; only the Go spelling
`-o=json` is accepted alongside `-o json`. `-v` prints the version at the top
level but is undefined on a verb, while `-h` prints usage on both. `--yes` has
no `-y`. 6 checks, 2 pass, 4 fail.

Runnables: `src:.ok-planner/experiments/assumption-cli-short-flags-single-dash/` at the stamped commit.
