---
audit: executor-reads-dispatch-context
artifact: story:executor-reads-dispatch-context
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:36Z
---

# Agent reads its dispatch identity and disposition at runtime

Supported. The dispatch RPC's request carries a dispatch id, a run-scope id, an optional prior-dispatch id, and a typed prior-dispatch disposition (one of exactly three named values: stale-recovery, retry-after-error, recalculate) on the wire. The reference agent executor captures these fields at spawn and exposes them to the driven script through a dedicated internal tool, whose description names all four fields and all three disposition values the story requires. Unit tests cover the wire-to-typed-disposition mapping for all 3 disposition values plus the unset case, and cover the tool's presence in the executor's full registered tool set and its call-and-response path.
