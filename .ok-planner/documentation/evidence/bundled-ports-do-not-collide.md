---
trap: bundled-ports-do-not-collide
release: d977250c
---
# Evidence set — every bundled service's default port is distinct, so the eleven service images plus the core stack come up together with no port configuration.

Source of the prior: craft-convention — a curated default-port table for a shipped service bundle

## What the audit ran and observed (assumption record)

Experiment `assumption-bundled-ports-do-not-collide` (six checks, none failing)
put the core stack and all eleven bundled services in one network namespace at
their default ports, then the host-agent proxy, at this tree's image tag. The
prior does not hold, by one service and one core image. Ten of the eleven
services came up; the filesystem claim producer exited with
`claim-producer-filesystem: grpc listen: listen tcp 0.0.0.0:9100: bind: address
already in use`, because its default gRPC port 9100 is the port the supervisor's
async-callback listener already holds in the same core stack. The host-agent
proxy exited the same way on `listen tcp :9090: bind: address already in use`,
its default agent-facing port being the claude-agent executor's default gRPC
port. The other nine services and the two core listeners (8080, 9100) shared the
host without complaint, so the default table is nearly distinct — and the two
overlaps are between a service and a core listener rather than between two
services, which is exactly where an operator reading the per-service defaults
would not look. Both are configuration away: the same producer image with
`grpc_port: 9200` and `http_port: 9210` in its config came up reporting
`grpc_addr [::]:9200`.

## Experiment record (experiment:assumption-bundled-ports-do-not-collide)

# The whole bundle on one host, nothing configured

## What it ran against

One network namespace shared by everything. A `rimsky-all-in-one` container
holds it and publishes every port of the bundle's default table; each of the
eleven bundled services then joins that namespace with `--network container:`
and its default ports, followed by the `rimsky-host-agent-proxy` image. The two
services that need backing state get it inside the same namespace: a postgres
container, and a host directory for the filesystem producer. Each service is
read once it has either exited or taken a port of its own — never on a clock.

## What was observed

Six checks, none failing.

The core stack takes two ports: 8080 for the control API and 9100 for the
supervisor's async-callback listener, both answering.

Ten of the eleven bundled services then came up. The eleventh, the filesystem
claim producer, exited: `claim-producer-filesystem: grpc listen: listen tcp
0.0.0.0:9100: bind: address already in use` — its default gRPC port is the port
the supervisor's callback listener already holds. Nothing else collided.

The host-agent proxy exited the same way on `listen tcp :9090: bind: address
already in use` — its default agent-facing port is the claude-agent executor's
default gRPC port.

Both are configuration away from working: the same producer image, given a
config naming `grpc_port: 9200` and `http_port: 9210`, came up and reported
`grpc_addr [::]:9200`.

Runnables: `src:.ok-planner/experiments/assumption-bundled-ports-do-not-collide/` at the stamped commit.
