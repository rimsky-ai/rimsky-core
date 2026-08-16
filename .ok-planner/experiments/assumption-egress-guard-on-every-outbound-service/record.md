---
experiment: assumption-egress-guard-on-every-outbound-service
commit: d977250c
---

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
