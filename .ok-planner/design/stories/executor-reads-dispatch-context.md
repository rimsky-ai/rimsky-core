---
story: executor-reads-dispatch-context
status: as-is
aliases: []
---

# Agent reads its dispatch identity and disposition at runtime

## Role and capability

As an agent author writing the script that drives a reference-executor node, I can read the dispatch's identity and disposition — the run-scope identifier, this dispatch's identifier, the prior dispatch's identifier (when there is one), and the prior dispatch's disposition (`stale_recovery`, `retry_after_error`, or `recalculate`) — through a dedicated read tool on the reference executor's agent surface, so my script can adapt to retry-after-error, stale-recovery, and recalculate paths without inferring from indirect signals. The four fields already arrive on the protocol's inbound dispatch payload. The reference executor exposes a first-class signal for the dispatch context the protocol already delivers.

## Acceptance

The reference executor exposes a read-only tool that returns the four dispatch-context fields captured from the inbound dispatch payload at spawn: the dispatch identifier (always present), the run-scope identifier (always present), the prior dispatch identifier (present on re-dispatch within a run-scope, omitted on the first dispatch), and the prior dispatch disposition (present whenever a prior dispatch identifier is present; one of `stale_recovery` / `retry_after_error` / `recalculate`). Values returned equal the inbound dispatch context verbatim. The tool's lifetime matches the per-run attribute-read snapshot — a stable read of the spawn-time inputs, not a live read of mutable state. The tool is read-only; emitting it does not change runtime behavior. The tool sits beside the existing per-run attribute-read on the agent surface (parallel idiom, independent return shape); the dispatch-context return is independent of the attribute snapshot, so additions to the inbound dispatch payload extend the dispatch-context return without overloading the attribute return.

## Falsifier

The agent has no first-class signal for which kind of dispatch it is running — fresh, retry-after-error, stale-recovery, or recalculate — because the per-run attribute snapshot carries only operator-supplied template attributes, not the wire-level dispatch identity. An agent script that wants to behave differently on retry-after-error has to infer from indirect signals (an attribute the operator chose to carry, a counter outside the protocol) or treat every dispatch as fresh and risk re-doing work the prior dispatch already completed. OR: a tool exists but the values returned drift from the inbound dispatch context, OR the tool returns mutable values that change mid-dispatch.

## Proof

Two-layer demo. **Reference-executor unit e2e** — the agent's MCP loop is driven by a fake CLI through four cases that cover the full disposition matrix at the agent-tool layer: a fresh dispatch (no prior fields), a retry-after-error dispatch carrying the prior dispatch identifier and `retry_after_error` disposition, a stale-recovery dispatch carrying the prior identifier and `stale_recovery`, and a recalculate dispatch carrying the prior identifier and `recalculate`. Each case asserts the agent observes the four context fields the tool returns. **End-to-end scenario** — a scenario test brings up rimsky plus the reference executor in containers, deploys a single-node template with the dispatch-context-probe agent script, and asserts the agent reads its dispatch identity and run-scope identity through the dispatch-context tool with no prior fields — equal verbatim to the dispatch context the supervisor recorded for that dispatch.
