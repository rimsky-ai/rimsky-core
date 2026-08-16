---
assumption: cli-short-flags-single-dash
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# single-dash short forms work the way `-v` and `-h` do, so `-f` is `--follow`/`--force`, `-o` is `--output`, and short forms cluster.

As operator at a terminal, I would take it that single-dash short forms work the way `-v` and `-h` do, so `-f` is `--follow`/`--force`, `-o` is `--output`, and short forms cluster.

## Source

craft-convention — `-v` and `-h` present in `cli-verbs`, `--f` and `--o` listed among `cli-flags`

## What a run would observe

invoke each verb with `-f` and `-o` and check whether the parser accepts them and which long flag they bind to.

## Measured

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
