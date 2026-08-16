---
audit: subscription-edges-only-from-explicit-block
artifact: decision:subscription-edges-only-from-explicit-block
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:33:05Z
---

# The edge map has exactly two insert sites: the subscribes block and the structural-root injection

Supported. The subscription-edge map is populated by one builder, and that builder holds the only two insert calls in the tree: one per entry in each node's subscribes block, and one synthetic edge per structural-root node keyed to the empty sender with a success-terminal pattern and upstream refresh off, exactly as described. Substitution references and message references are parsed by the same builder but only to decide whether a node has an upstream — neither contributes an edge, and two dedicated tests assert that a node reading another node's attribute and a node reading a message body each register no edge and no sender key. The structural-root test in the same file covers the injected edge's sender, pattern, and refresh flag. The disqualification set matches the decision term for term: a non-self subscribes entry, an upstream attribute reference, or a message-body reference each disqualifies, and a self-subscription is skipped. Sub-graph internal nodes are excluded from the injection, which is the sibling injection decision's territory rather than a departure from this one. The map is derived per template from the registered spec and memoized by template hash, so the injection is template-determinable and carries no per-instance or operator-facing surface.
