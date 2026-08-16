---
trap: cli-context-flags-everywhere
release: d977250c
demonstration: experiment:assumption-cli-context-flags-everywhere
---
## Assumption

As operator with several deployments, I would take it that `--control-api`, `--api-key`, and the context selection apply uniformly to every verb, so any verb can be pointed at any endpoint without switching context first.

published-concept — `concept:rimsky` ("every verb accepts an API-key flag and falls back to an API-key environment variable")

## Actual behavior

the experiment — built for
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
