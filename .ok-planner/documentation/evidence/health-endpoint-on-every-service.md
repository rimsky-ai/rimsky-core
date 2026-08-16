---
trap: health-endpoint-on-every-service
release: d977250c
---
# Evidence set — every rimsky-shipped process answers `GET /health`, since the supervisor callback listener and the webhook sensor both do.

Source of the prior: sibling-symmetry — two `/health` entries in `http-routes` covering only the supervisor listener and sensor-webhook

## What the audit ran and observed (assumption record)

Experiment `assumption-health-endpoint-on-every-service` (six checks, none
failing) probed `GET /health` on every port the shipped images document: a
`rimsky-all-in-one` container's control API and supervisor callback listener,
all eleven bundled services, and the host-agent proxy. The prior does not hold.
Exactly two listeners in the whole shipped set answer it — the supervisor's
callback listener (200, `ok`) and `sensor-webhook`'s HTTP ingress on 9184. The
near misses are spelled differently rather than absent: the control API answers
404 at `/health` and 200 at `/v1/health`, and the claude-agent HTTP bridge
answers 404 at `/health` and 200 at `/healthz`. The openlineage subscriber
serves no HTTP at all and the proxy's agent-facing port is gRPC-only, so for
those two there is no HTTP probe to write. An operator who writes one k8s probe
spec and applies it to every rimsky pod gets two working probes, two that need a
different path, and two processes with no HTTP liveness surface at all.

## Experiment record (experiment:assumption-health-endpoint-on-every-service)

# Which shipped listeners answer GET /health

## What it ran against

The tree's own images: one `rimsky-all-in-one` container with both its HTTP
listeners published (the control API on 8080 and the supervisor's async-callback
listener on the baked 9100), all eleven bundled services with every port they
document published, and the `rimsky-host-agent-proxy` image pointed at the
all-in-one stack. The filesystem producer runs against a mounted policy config;
the postgres producer and the openlineage subscriber run against a postgres
container. Each subject is read once its first port accepts a connection — the
subscriber, which opens no port, once it has created its cursor table.

## What was observed

Six checks, none failing.

Two listeners in the whole shipped set answer `GET /health`. The supervisor's
callback listener answers 200 with `ok`, and among the eleven bundled services
exactly one port does: `sensor-webhook`'s HTTP ingress on 9184. Nothing else
does.

The near misses are spelled differently rather than missing. The control API
answers 404 at `/health` and 200 at `/v1/health`. The claude-agent HTTP bridge
answers 404 at `/health` and 200 at `/healthz`. The openlineage subscriber
serves no HTTP at all, and the host-agent proxy's agent-facing port is
gRPC-only, refusing the probe outright.

Runnables: `src:.ok-planner/experiments/assumption-health-endpoint-on-every-service/` at the stamped commit.
