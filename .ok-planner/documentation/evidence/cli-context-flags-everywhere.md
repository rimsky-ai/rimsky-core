---
trap: cli-context-flags-everywhere
release: d977250c
---
# Evidence set — `--control-api`, `--api-key`, and the context selection apply uniformly to every verb, so any verb can be pointed at any endpoint without switching context first.

Source of the prior: published-concept — `concept:rimsky` ("every verb accepts an API-key flag and falls back to an API-key environment variable")

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-context-flags-everywhere` — built for
this run — asked the parser which of `--control-api`, `--api-key`,
`--endpoint`, and `--key` each of 26 verbs takes (one per family), then
pointed one verb per family at a live authenticated `rimsky-all-in-one` from
this tree's image set with no context configured and no environment fallback.

The two names the prior uses are not the CLI's. `--control-api` is accepted by
1 of the 26 verbs — `conformance publisher`, where it names a control API to
poll — and `--api-key` by 1, `agent start`, where it is the plaintext the host
agent presents to the proxy. The general spellings are `--endpoint` and
`--key`, and even they are not universal: `ctx current`, `agent status`,
`agent start`, and `compose run` take neither, and `conformance executor`
takes `--endpoint` but not `--key`.

The sharper contradiction is a flag that parses and is dropped. `rimsky
compose status --endpoint <url> --key <valid admin key>` accepts both flags
and then sends the request with no credential: `GET /v1/tags: 401
unauthorized`. `RIMSKY_API_KEY` in the environment fails the same way, so the
whole compose family — `up`, `down`, `plan`, `status` — cannot authenticate
against any deployment that has left anonymous mode, by flag or by variable.
That is the operator's silent case: the flag was accepted, so nothing warns
them, and the 401 reads as a server problem rather than a dropped credential.

The concept the prior draws on says every verb accepts an API-key flag and
falls back to an API-key environment variable; measured at this tree, the
compose family does neither. 2 checks, 0 pass, 2 fail.

## Experiment record (experiment:assumption-cli-context-flags-everywhere)

# Whether the endpoint and api-key flags reach every verb

## What it ran against

The CLI built from this tree, and one `rimsky-all-in-one` container from this
tree's image set on a free port. Stage 1 asks the parser which of four
spellings — `--control-api`, `--api-key`, `--endpoint`, `--key` — each of 26
verbs takes, one per family, with the endpoint pointed at a closed port so
"connection refused" means accepted. Stage 2 asks whether the accepted ones
are honoured: with no context configured and no environment fallback, one verb
per family is pointed at a live authenticated deployment explicitly, and a 401
is read as the flag having been parsed and dropped.

## What was observed

The two names the prior uses barely exist. `--control-api` is accepted by 1 of
26 verbs (`conformance publisher`, where it names the control API to poll for a
pushed message) and `--api-key` by 1 (`agent start`, where it is the plaintext
the host agent presents to the proxy on Register). The CLI's own names are
`--endpoint` and `--key`, accepted by 22 and 20 of the 26.

The families that take neither: `ctx current`, `agent status`, `agent start`,
and `compose run` reject all four; `conformance executor` takes `--endpoint`
but not `--key`.

Where the flags do parse they are mostly honoured — `template list`, `instance
list`, `auth list`, and `parked list` all authenticated against the live
deployment on `--endpoint` plus `--key` alone. The exception is the compose
family: `compose status --endpoint <url> --key <valid admin key>` parses both
flags, sends the request with no credential, and comes back
`GET /v1/tags: 401 unauthorized`. The same command with `RIMSKY_API_KEY` set
in the environment fails identically, so neither the flag nor the environment
fallback reaches it. 2 checks, 0 pass, 2 fail.

Runnables: `src:.ok-planner/experiments/assumption-cli-context-flags-everywhere/` at the stamped commit.
