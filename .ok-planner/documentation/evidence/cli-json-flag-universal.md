---
trap: cli-json-flag-universal
release: d977250c
---
# Evidence set — `--json` is accepted by every read verb and emits parseable JSON on stdout with all human chatter on stderr, so `rimsky … --json | jq` never breaks.

Source of the prior: craft-convention — a single global `--json` flag in a CLI whose read verbs are numerous

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-json-flag-universal` — built for this
run — asked the shipped CLI's parser about `--json` on 30 read verbs (every
listing, get, status, and tail verb in `rimsky --help`, across the control-api,
auth, ctx, agent, and compose families), then booted one `rimsky-all-in-one`
from this tree's image set to capture stdout and stderr separately for the
verbs that accept it.

Exactly one of the 30 accepts `--json`: `rimsky auth list`. The other 29 —
including every control-api verb, where a JSON mode does exist under the
spelling `-o json` — reject it with `flag provided but not defined: -json`,
exit 2, and an empty stdout. So `rimsky ls templates --json | jq` does not
merely print human text into the pipe; it prints nothing and fails. A user who
learns `--json` on the first verb they try is wrong about the next 29, and the
verb that taught it (`auth list`) is the one that will not take `-o json`.

The prior's second clause holds wherever the flag exists: `auth list --json`
put a JSON document on stdout with an empty stderr, as did `ls templates
-o json`. The exception is the third home of the name — `compose run --json`
means JSON Lines on **stderr**, and the run observed 13.5 KB there against an
empty stdout, so the one long-running verb inverts the placement the prior
assumes. 4 checks, 1 pass, 3 fail.

## Experiment record (experiment:assumption-cli-json-flag-universal)

# `--json` across the CLI's read verbs

## What it ran against

The CLI built from this tree. Stage 1 asks only the parser and needs no
server: the endpoint points at a closed port, so a connection refusal means
the flag was accepted and "flag provided but not defined" means it was not.
The population is 30 read verbs — every listing, get, status, and tail verb in
`rimsky --help`, across the control-api, auth, ctx, agent, and compose
families. Stage 2 boots one `rimsky-all-in-one` from this tree's image set on
a free port, mints an admin key, and captures stdout and stderr into separate
files for the verbs that do accept `--json`, plus `compose run --json`, which
self-hosts rimsky in process.

## What was observed

One of the 30 read verbs accepts `--json`: `rimsky auth list`. The other 29
reject it outright with "flag provided but not defined: -json", exit 2, and an
empty stdout — including every verb in the control-api family, where the JSON
mode is spelled `-o json` instead. Where `--json` does exist it behaves as the
prior expects: `auth list --json` put a 200-byte JSON document on stdout and
nothing on stderr, and `ls templates -o json` did the same. `compose run
--json` is the third home of the name and inverts the placement: 13.5 KB of
JSON Lines on stderr, stdout empty. 4 checks, 1 pass, 3 fail.

Runnables: `src:.ok-planner/experiments/assumption-cli-json-flag-universal/` at the stamped commit.
