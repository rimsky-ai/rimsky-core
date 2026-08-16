---
trap: bundled-ports-do-not-collide
release: d977250c
demonstration: experiment:assumption-bundled-ports-do-not-collide
---
## Assumption

As operator running the full bundle on one host, I would take it that every bundled service's default port is distinct, so the eleven service images plus the core stack come up together with no port configuration.

craft-convention — a curated default-port table for a shipped service bundle

## Actual behavior

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
