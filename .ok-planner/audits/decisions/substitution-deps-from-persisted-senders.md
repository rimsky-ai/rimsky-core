---
audit: substitution-deps-from-persisted-senders
artifact: decision:substitution-deps-from-persisted-senders
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Substitution deps are read from the persisted attribute store, scoped to the receiver's own frame, with wait-set rows only pinning which sender run to read

Supported. `populateSubscribedSenderDeps` (`lib/runtime/substitution_context.go`) enumerates the receiver's subscribed sender node types from the subscription-edge map, then for each sender either uses the wait-set-pinned settled run (`pinnedSenderRunsForReceiver`, built from wait-set rows keyed by the receiver's own frame) or falls back to `GetMostRecentSettledRun` scoped by `run_scope_id` — never by signal/cascade-emission presence — and reads the persisted attribute row via `senderAttrsRaw`. Wait-set rows carry no attribute payload in this code path; they are consulted only to select a sender run identity. The `run_scope_id` scoping is frame-safe because the concept doc for run-scope states a RunScope lives inside exactly one frame and RunScopes never span frames — the receiver's own `RunScopeID` is used throughout, so no cross-frame row can be reached. The wake-vs-data split and the sequenced-queued-round pinning behavior are covered end to end by a dedicated scenario test for the round-pinning story this decision cites.
