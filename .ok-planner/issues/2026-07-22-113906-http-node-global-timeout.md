---
issue: http-node-global-timeout
kind: human
category: config-surface
artifacts:
  - lib/services/executors/http-node/config.go
status: verified
opened: 2026-07-22T11:39:06Z
---

# The per-node deadline you can already set is silently capped by a timeout you can't see

Rimsky's built-in `http-node` executor — the worker service that makes an outbound HTTP call on behalf of a workflow node — cuts every call off at one deployment-wide timeout (an env var, default 60 seconds). An author whose node legitimately needs longer — a bulk export, a big download — has no per-node lever; the only remedy raises the ceiling for every `http-node` call in the deployment. The filed request: add a per-node timeout field. But re-verification found the sharper fact underneath: the platform *already has* the per-node mechanism, and it's being defeated by a bug.

Here's the mechanism. Any node can declare a per-node deadline (`sync_rpc_deadline`) in the workflow template, and that deadline already travels down into `http-node`'s outbound call as a cancellation signal — the design decision governing deadlines (`decision:three-dispatch-deadlines`) explicitly made this the one mechanism and argued against per-executor timeout knobs beside it. The catch: `http-node` also constructs its HTTP client with its own independent timeout from the env var, and whichever limit fires first wins. So raising a node's deadline past 60 seconds today changes nothing — the invisible lower ceiling still kills the call. The precedent the filer pointed to (the `claude-agent` executor's own per-node timeout fields) solves a different problem — detecting a stuck subprocess in a long background run — not bounding a synchronous call, which is exactly what the existing deadline mechanism was built for.

## Options

- **Fix the bug, no new config**: drop the client's independent timeout so the per-node deadline is the sole bound; the deployment-wide lever becomes the existing deployment-level deadline default. The env var retires.
- **Add the requested per-node field** (claude-agent style), demoting the env var to its default — a working knob, at the cost of a second timeout concept covering ground the deadline mechanism already owns.
- **Repurpose the env var** to bound only a sub-phase (e.g. connection setup) while the deadline bounds the whole call — keeps an operator safety knob without the conflict, but the standard HTTP client doesn't support it out of the box.

The ruling decides: fix the existing mechanism or add a dedicated field; what happens to the env var; and whether the double-ceiling gets fixed as a bug regardless.

## Ruling

> Recommended ruling (/recommend-rulings): Make the per-node
> sync_rpc_deadline the sole bound on the outbound call: remove the
> http.Client.Timeout double-ceiling as a bug, and retire
> RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS — the deployment-wide lever is
> the existing SyncRPCDeadlineDefault. No new config surface.
>
> Rationale: decision:three-dispatch-deadlines already owns this job;
> a second, invisible ceiling under the declared deadline is two
> idioms for one job and today makes raising sync_rpc_deadline past
> 60s silently ineffective — a bug in its own right.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
