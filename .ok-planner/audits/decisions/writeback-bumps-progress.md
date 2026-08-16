---
audit: writeback-bumps-progress
artifact: decision:writeback-bumps-progress
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:39:17Z
---

# The attribute-writeback callback bumps the dispatch row's progress timestamp in the write's own transaction

Supported. Exactly one mid-dispatch attribute writeback callback exists — the per-run attributes route on the supervisor's callback listener, registered beside the keepalive route and reachable only with the dispatch's cancel token — and its handler opens a single transaction that locks the dispatch row, refuses a row that is not running or held, applies the delta as an insert or a merge onto the per-run attribute ledger, then bumps the progress timestamp and renews the claim expiry before committing; nothing in the handler commits the attribute write separately, so a failed bump rolls the write back with it. The column it bumps is the same last-progress column the async orphan sweep reads for its quiet-period check, so the writeback does serve as the liveness signal the decision claims, and no separate keepalive call is required alongside it. A test nulls the column, posts a writeback, and asserts both that the delta landed on the ledger and that the timestamp is set, with a second post confirming delta-merge semantics.
