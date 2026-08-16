---
trap: cli-json-flag-universal
release: d977250c
demonstration: experiment:assumption-cli-json-flag-universal
---
## Assumption

As operator scripting against the CLI, I would take it that `--json` is accepted by every read verb and emits parseable JSON on stdout with all human chatter on stderr, so `rimsky … --json | jq` never breaks.

craft-convention — a single global `--json` flag in a CLI whose read verbs are numerous

## Actual behavior

the experiment — built for this
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
