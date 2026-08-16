---
experiment: assumption-log-level-universal
commit: d977250c
---

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
