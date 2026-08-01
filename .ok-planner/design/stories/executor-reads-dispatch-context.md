---
story: executor-reads-dispatch-context
status: as-is
aliases: []
---

# Agent reads its dispatch identity and disposition at runtime

## Story

As an agent author writing the script that drives a reference-executor node, I can read the dispatch's identity and disposition — the run-scope identifier, this dispatch's identifier, the prior dispatch's identifier (when there is one), and the prior dispatch's disposition (`stale_recovery` or `recalculate`) — through a dedicated read tool on the reference executor's agent surface, so my script can adapt to stale-recovery and recalculate paths without inferring from indirect signals. The four fields already arrive on the protocol's inbound dispatch payload. The reference executor exposes a first-class signal for the dispatch context the protocol already delivers. Retry is not a dispatch-context concern under `decision:in-place-retry`: retries loop in-process on the same dispatch row with the same dispatch_id, so an agent never sees a "this is a retry" disposition at the dispatch-context layer.
