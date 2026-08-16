---
assumption: egress-guard-on-every-outbound-service
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every bundled service that dials a caller-supplied or config-supplied URL is behind the same default-closed SSRF guard, including the claude-agent executor and the OpenLineage subscriber.

As security reviewer, I would take it that every bundled service that dials a caller-supplied or config-supplied URL is behind the same default-closed SSRF guard, including the claude-agent executor and the OpenLineage subscriber.

## Source

sibling-symmetry — `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` and `RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST` with no third

## What a run would observe

point `RIMSKY_OPENLINEAGE_BACKEND_URL` at a link-local address and see whether the dial is refused.

## Measured

Experiment `assumption-egress-guard-on-every-outbound-service` (seven checks,
none failing) put a recording HTTP sink at a private-range address on a docker
network beside a postgres-backed `rimsky-all-in-one` stack and the openlineage
subscriber, and gave the same private URL to two bundled executors. Experiment
`sensor-http` was re-run at this tree (eleven checks, none failing) for the
second guarded service. The prior does not hold.

The guard is wired into exactly two of the outbound dialers. `http-node`
refused the private-range URL with the guard's own message — `egress:
destination 172.21.0.3 is in a blocked range (loopback/private/link-local/
metadata) and not in the operator egress allowlist` — and the sink recorded
nothing from it; naming that address in
`RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` let the same URL through on the
next run. `sensor-http` behaves the same way: its re-run shows a private-network
poll target refused until the operator allowlists the range.

Two other bundled services dial caller- or config-supplied URLs with nothing in
the way. `verifier-http` — an executor taking its `url` from node attributes
exactly as `http-node` does — dialed the same private address and was answered,
ending `terminal/success`, and it has no allowlist variable at all. The
openlineage subscriber posted lineage to the private-range sink, and when
pointed at `169.254.169.254` it attempted that dial too, failing with a client
timeout on `http://169.254.169.254/openlineage/api/v1/lineage` rather than an
egress refusal. The claude-agent executor was not measured here for its own
dials; the re-run of `claude-agent-mcp-servers-per-node` shows it hands
node-declared MCP server URLs (loopback ones, in that experiment) to the CLI
unexamined.

A security reviewer who reads the two `_EGRESS_ALLOWLIST` variables as the
deployment's SSRF posture is covering `http-node` and `sensor-http` only, and a
template author reaching an internal address through `verifier-http` is not
bounded by either of them.
