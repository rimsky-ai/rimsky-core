---
story: executor-reads-dispatch-context
status: as-is
aliases: []
---

# Agent reads its dispatch identity and disposition at runtime

## Story

As an agent author writing the script that drives a reference-executor node, I can read the dispatch's identity and disposition — which run-scope and dispatch this is, whether a prior dispatch preceded it, and whether that predecessor was recovered or recalculated — so that my script adapts to recovery and re-run paths without inferring them from indirect signals.
