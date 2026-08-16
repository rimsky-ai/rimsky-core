---
trap: log-level-universal
release: d977250c
---
# Evidence set — `RIMSKY_LOG_LEVEL` is honored by all eight binaries and all eleven bundled service images, with the same level vocabulary.

Source of the prior: sibling-symmetry — a single un-namespaced `RIMSKY_LOG_LEVEL` in an env set where everything else is namespaced per service

## What the audit ran and observed (assumption record)

Experiment `assumption-log-level-universal` (eleven checks, none failing) ran
every binary and image at this tree's tag that can be reached through the
public surface: five `rimsky-all-in-one` containers, one per level value; the
`rimsky-host-agent-proxy` image; the host agent through the public
`rimsky agent start --foreground` verb; and all eleven bundled service images.
Each reading waits on an external milestone — a completed observability refresh
visible on `GET /v1/observability/executors`, a port accepting a connection, a
postgres table appearing — never on a clock.

The prior does not hold, in two ways. First, the population: the core process
honors the variable (at `error` it silences every INFO and WARN line it
otherwise emits; at `debug` it adds DEBUG lines) and so does the proxy image,
but **none of the eleven bundled services does** — all ten with listeners had
logged their INFO startup line before they accepted a connection while told
`error`, and `subscriber-openlineage` logged `openlineage.starting` at INFO
under the same setting. The host agent started through `rimsky agent start`
ignores it too, and prints stdlib plain-text lines rather than the JSON records
the core roles emit, so the format is not uniform either. The standalone
`rimsky-host-agent` binary ships in no image and this run did not exercise it.

Second, the vocabulary: the accepted values are `debug`, `warn`, `error` and
nothing else. `DEBUG` and `trace` both fell back to the default level silently —
53 INFO lines, no DEBUG lines, and the offending value named nowhere in the log.

An operator who sets `RIMSKY_LOG_LEVEL=debug` across a stack gets more detail
from the core roles and the proxy, unchanged output from every bundled service,
and — if the value is capitalized or borrowed from another product's vocabulary
— nothing at all, without being told.

## Experiment record (experiment:assumption-log-level-universal)

# Who listens to RIMSKY_LOG_LEVEL

## What it ran against

Everything the tree's own images and CLI binary can be made to run. Five
`rimsky-all-in-one` containers, one per level value (`unset`, `error`, `debug`,
`DEBUG`, `trace`), each with a config mounted that declares one remote executor
and a one-second observability refresh, so a completed refresh cycle — visible
from outside as the executor's `last_probed_at` advancing on
`GET /v1/observability/executors` — is the milestone every reading waits for
rather than a clock. One `rimsky-host-agent-proxy` container pointed at that
stack, read once its gRPC port accepts a connection. The host agent started
through the public `rimsky agent start --foreground` verb with
`RIMSKY_AGENT_LISTEN` at a chosen port, read once that port accepts a
connection. All eleven bundled service images: the ten with listeners read once
their port accepts a connection, and `subscriber-openlineage`, which has no
port, run against a postgres container and read after it has created its cursor
table and been stopped.

The standalone `rimsky-host-agent` binary is in no shipped image, so the run
reaches the host agent through the CLI verb instead.

## What was observed

Eleven checks, none failing.

The core process honors the variable in the downward direction and one value
upward. At the default level all five subjects inside it — entrypoint, migrate,
scheduler, supervisor, control-api — log at INFO; `error` silences every INFO
and WARN line the same container otherwise emits; `debug` adds DEBUG lines the
default level withholds. The vocabulary is exactly `debug`, `warn`, `error` and
anything else: `DEBUG` and `trace` both fell back to the default level, silently
— 53 INFO lines, no DEBUG lines, and neither value named anywhere in the log.

The proxy image honors it: told `error`, it had emitted no INFO line by the time
its port was accepting connections.

The host agent does not, on the path the CLI offers. Started by
`rimsky agent start` with `RIMSKY_LOG_LEVEL=error`, it logged `hostagent
starting` at INFO and a connect-failure WARN, in the stdlib's plain-text format
rather than the JSON the core roles emit — a second vocabulary in the same
stack.

Not one of the eleven bundled services honors it. All ten with listeners had
already logged their `INFO` startup line by the time they accepted a
connection, and the eleventh logged `openlineage.starting` at INFO as well —
every one of them told `error`.

Runnables: `src:.ok-planner/experiments/assumption-log-level-universal/` at the stamped commit.
