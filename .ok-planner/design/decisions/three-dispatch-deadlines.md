---
decision: three-dispatch-deadlines
status: as-is
aliases: []
---

# Three orthogonal dispatch deadlines

## Choice

Three independent deadlines govern each dispatch: `sync_rpc_deadline` (bounds a synchronous dispatch's outbound call), `max_quiet_period` (maximum time between liveness signals during an async dispatch), and `max_runtime` (absolute wall-clock bound on the dispatch). All three are node-template fields with deployment-config-supplied defaults; there is no separate executor-level declaration layer for deadlines, and `0` is the disable sentinel — a zero-valued deadline is not applied. The per-node `sync_rpc_deadline` is the **sole** bound on a synchronous dispatch's outbound call: no executor-internal client timeout may sit beneath it, and the deployment-wide lever is the deadline's deployment-config default, not a per-executor timeout knob.

## Rationale

Sync dispatches need a deadline on the RPC itself; async dispatches need a way to detect "executor went quiet"; both benefit from an absolute upper bound for safety. The three answer different questions and so need to be independently configurable. `0 = disabled` is necessary for workloads where artificial caps are anti-features (LLM-driven work, multi-day human review). A second, executor-internal ceiling under the declared deadline is two idioms for one job and silently defeats the declared one — raising a node's deadline past the hidden ceiling changes nothing, which is a bug, not a knob.

## Alternatives

Single unified deadline — rejected because it conflates three orthogonal concerns. `0` meaning "use default" — rejected because it collides with the disable semantic that the LLM / human-review use cases need. A per-executor timeout layer beneath the node deadlines — rejected: covers ground the deadline mechanism already owns and reintroduces the invisible second ceiling.
