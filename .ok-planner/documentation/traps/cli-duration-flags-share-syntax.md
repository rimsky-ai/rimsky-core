---
trap: cli-duration-flags-share-syntax
release: d977250c
demonstration: experiment:assumption-cli-duration-flags-share-syntax
---
## Assumption

As operator setting timeouts, I would take it that every duration-shaped flag and config key (`--timeout`, `--grace`, `--poll-interval`, `--older-than`, `--expires`, `dispatch_defaults.*`, `retention.*`) accepts the same duration grammar, e.g. `30s`, `5m`, `24h`.

craft-convention — a uniform duration vocabulary across one product's flags and config

## Actual behavior

the experiment — built
for this run — fed one vocabulary (`30s`, `5m`, `24h`, `1h30m`, `500ms`,
`30d`, `1w`) to every duration-shaped surface the prior names, across three
instruments: the parser for the eight locally parsed flags, a live
`rimsky-all-in-one` for the two server-parsed ones, and a container boot per
config file for the config keys.

There are three grammars, not one. The eight locally parsed flags
(`--poll-interval` on `instance events` / `messages tail` / `watch`,
`--older-than` on `parked list` / `lineage prune`, `--timeout` on `run` /
`compose run` / `conformance executor`) agree with each other exactly: Go's
duration grammar, so `30s` / `5m` / `24h` / `1h30m` / `500ms` parse and `30d`
and `1w` do not. `auth create-key --expires` is the second grammar: it accepts
`30d`, and its own help advertises "e.g. 24h, 30d". Its sibling in the same
family, `auth rotate --grace`, is the third: it rejects `30d`, and rejects it
server-side as `400 invalid grace duration` rather than as a local parse
error. Two flags in one command family, both naming a key lifetime, disagree
on whether `30d` is a duration.

The config keys follow Go's grammar, not `--expires`'s, and fail hard:
`dispatch_defaults.sync_rpc_deadline: 30s|5m|24h` boots healthy, while `30d`
never comes up at all — `rimsky-migrate` exits with `cannot unmarshal !!str
'30d' into time.Duration` and the entrypoint reports `migrate failed`. So the
literal an operator learns from `--expires` is the one that turns a config
file into a container that will not start.

One further duration-shaped flag, not named in the prior:
`--retention-test-seconds` takes a bare integer and rejects `30s` and `5m`
outright. The three example values the prior offers do work everywhere except
there; the grammars behind them do not match. 2 checks, 0 pass, 2 fail.
