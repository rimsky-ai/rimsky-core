---
experiment: assumption-cli-short-flags-single-dash
commit: d977250c
---

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
