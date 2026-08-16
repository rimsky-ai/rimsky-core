---
experiment: assumption-cli-json-flag-universal
commit: PENDING
---

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
