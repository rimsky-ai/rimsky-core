---
audit: cascade-inside-settlement
artifact: decision:cascade-inside-settlement
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:03:38Z
---

# The parent-settlement cascade bridge fires inside the settle primitive, not at its call sites

Supported for both settle primitives — the two the artifact names, carry and aggregate, are the complete set. Carry's primitive closes the sub-graph run-scope and then, in its own body, loads the calling node, emits the bridge signal to that node's subscribers, emits its attribute-change signals, and drains its wait set; the function even refuses to proceed when the node table is unavailable, naming the bridge as the reason. Aggregate's primitive settles the parent claim through the shared claim-terminal resolution in its own body, and that resolution fires the deferred held cascade for the parent's holder runs, which is the parent node's settlement cascade for the fan-out shape. Each primitive has exactly one call site, and neither call site fires any cascade alongside the call — checked by reading both, so the defect class the decision excludes has no instance to exclude. End-to-end coverage exists on both paths: a scenario driving a downstream node that wakes only through the sub-graph exit, and fan-out scenarios asserting the cascade after aggregation under both the success and the strict-policy shapes.
