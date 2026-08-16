---
trap: health-endpoint-on-every-service
release: d977250c
demonstration: experiment:assumption-health-endpoint-on-every-service
---
## Assumption

As operator writing k8s probes, I would take it that every rimsky-shipped process answers `GET /health`, since the supervisor callback listener and the webhook sensor both do.

sibling-symmetry — two `/health` entries in `http-routes` covering only the supervisor listener and sensor-webhook

## Actual behavior

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
