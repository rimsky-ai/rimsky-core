---
trap: cli-duration-flags-share-syntax
release: d977250c
---
# Evidence set — every duration-shaped flag and config key (`--timeout`, `--grace`, `--poll-interval`, `--older-than`, `--expires`, `dispatch_defaults.*`, `retention.*`) accepts the same duration grammar, e.g. `30s`, `5m`, `24h`.

Source of the prior: craft-convention — a uniform duration vocabulary across one product's flags and config

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-duration-flags-share-syntax` — built
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

## Experiment record (experiment:assumption-cli-duration-flags-share-syntax)

# One duration vocabulary across the flags and config keys

## What it ran against

Seven duration literals — `30s`, `5m`, `24h`, `1h30m`, `500ms`, `30d`, `1w` —
fed to every duration-shaped surface the assumption names, on three
instruments. The eight locally parsed flags (`--poll-interval` on `instance
events` / `messages tail` / `watch`, `--older-than` on `parked list` /
`lineage prune`, `--timeout` on `run` / `compose run` / `conformance
executor`) are settled by the parser with no server. The two server-parsed
flags (`auth create-key --expires`, `auth rotate --grace`) are driven against
a live `rimsky-all-in-one` from this tree's image set. The config keys are
measured by booting a container per config file and asking whether it comes up
healthy.

## What was observed

The eight locally parsed flags agree with each other exactly: `30s`, `5m`,
`24h`, `1h30m`, `500ms` all parse; `30d` and `1w` are rejected, which is Go's
duration grammar and has no day or week unit.

`auth create-key --expires` does not agree with them. It accepts `30d` — its
own help advertises "e.g. 24h, 30d" — while every other flag rejects it. Its
sibling in the same family, `auth rotate --grace`, is a third case: it rejects
`30d`, but server-side, coming back as `400 invalid grace duration` rather
than a local parse error. So within the `auth` family, two flags that both
name a key lifetime disagree on whether `30d` is a duration.

The config keys follow Go's grammar, not `--expires`'s:
`dispatch_defaults.sync_rpc_deadline` set to `30s`, `5m`, or `24h` boots
healthy, and set to `30d` the container never comes up — `rimsky-migrate`
fails with `cannot unmarshal !!str \`30d\` into time.Duration` and the
entrypoint reports `migrate failed`. An operator who learns `30d` from
`--expires` writes an unbootable config.

One further surface, not named in the prior but duration-shaped:
`--retention-test-seconds` takes a bare integer and rejects `30s` and `5m`
outright. 2 checks, 0 pass, 2 fail.

Runnables: `src:.ok-planner/experiments/assumption-cli-duration-flags-share-syntax/` at the stamped commit.
