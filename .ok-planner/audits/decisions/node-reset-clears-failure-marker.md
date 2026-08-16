---
audit: node-reset-clears-failure-marker
artifact: decision:node-reset-clears-failure-marker
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:48:37Z
---

# Reset as an observability verb: gate preserved, marker cleared, nothing dispatched

Supported. The handler does exactly the three things the decision claims and no fourth. It first looks up the run scope holding a failed terminal run for the node and, finding none, returns a conflict response whose message says reset is valid only when the node has a failed terminal run in some scope — the state gate, preserved. On a valid reset it runs one statement that selects the most recent failed run in that scope and nulls its settling-signal column, which is the field the node-inspect surface reads to report a settled failure, then appends an operator-override audit row and returns success. Reading the whole handler confirms the three negatives: no message is enqueued, no frame is created, and no run state, dispatch row, or queue entry is touched, so dispatch eligibility is untouched by construction. The retry-budget claim behind the rationale holds too — nothing in the reset path reads or writes a budget, and there is no cross-run budget row for it to act on. The two-step operator workflow is exercised end to end by a scenario test that resets the failed node, observes that the reset alone changes nothing, then sends a message to drive a fresh dispatch, and finally asserts the failed run's settling-signal column is null; a second suite covers the conflict rejection on a node with no failed terminal run.
