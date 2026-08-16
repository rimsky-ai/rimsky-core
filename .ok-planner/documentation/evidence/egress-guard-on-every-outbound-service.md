---
trap: egress-guard-on-every-outbound-service
release: d977250c
---
# Evidence set — every bundled service that dials a caller-supplied or config-supplied URL is behind the same default-closed SSRF guard, including the claude-agent executor and the OpenLineage subscriber.

Source of the prior: sibling-symmetry — `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` and `RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST` with no third

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-egress-guard-on-every-outbound-service)

# Which outbound dialers the SSRF guard is wired into

## What it ran against

A private docker network carrying a postgres container, a `rimsky-all-in-one`
container running against it, a recording HTTP sink (`sink.py` under
`python:3.12-alpine`, which answers every request and logs method, path and
headers at `/_log`), and the `rimsky-subscriber-openlineage` image. The sink's
address on that network is a private-range address — the run checks that first,
because the whole reading depends on it.

One template gives two bundled executors the same private-range URL: an
`http-node` node and a `verifier-http` node. The run drives it with no egress
allowlist, then restarts the stack with
`RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` naming the sink's address and drives
it again. The subscriber is then pointed at the same sink, and finally at
`169.254.169.254`, the cloud-metadata address the guard names.

## What was observed

Seven checks, none failing.

The guard is real where it is wired in. With no allowlist, the `http-node`
dispatch ended `terminal/error/http/network_error` whose message is the guard's
own — `egress: destination 172.21.0.3 is in a blocked range
(loopback/private/link-local/metadata) and not in the operator egress
allowlist` — and the sink recorded no request from it. Allowlisting that address
let the same URL through on the next run, so the guard, not the network, was the
refusal.

The same private-range URL on a `verifier-http` node was dialed and answered:
the sink recorded the request and the node ended `terminal/success`. That
executor takes its URL from node attributes exactly as `http-node` does, and has
no guard and no allowlist variable.

The openlineage subscriber is unguarded too. Pointed at the private-range sink,
it posted lineage there — three POSTs of OpenLineage events. Pointed at
`169.254.169.254`, it attempted the dial rather than refusing it: the failure it
logged is `openlineage.emit_failed` with a client timeout on
`http://169.254.169.254/openlineage/api/v1/lineage`, with no egress refusal
anywhere in it.

Runnables: `src:.ok-planner/experiments/assumption-egress-guard-on-every-outbound-service/` at the stamped commit.
