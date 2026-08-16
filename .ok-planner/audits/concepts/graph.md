---
audit: graph
artifact: concept:graph
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:36:14Z
---

# Whether the four graph invariants hold in the template validator and the run-scope binding

Supported on all four. The registration validator enforces the reserved top-level graph two ways: a template using the explicit graphs surface is rejected when no graph carries the reserved name, and a second graph claiming that name is rejected as a duplicate name, so "exactly one" holds from both directions; the implicit form is the early return when no graphs are declared, and declaring both surfaces at once is its own rejection. Sub-graph shape is enforced positively — a non-reserved graph missing an entry point or an exit point is rejected, as is one whose entry equals its exit — and the top-level graph declaring either is rejected by name. Twenty-eight validator tests cover this file, of which nine target these four invariants directly, plus two end-to-end scenario tests for the top-level entry and exit rejections. The run-scope binding holds at the one site that creates a root scope, which stamps the reserved name unconditionally; the only other scope-creation site is the delegation path, which stamps the child graph's own name. The no-independent-entry-point claim is enforced at registration: structural root-edge injection skips every node declared inside a non-reserved graph, and separate checks reject an outer graph subscribing to, holding from, or otherwise referencing a sub-graph's internals, so a sub-graph node is reachable only through the delegation that opens its scope.
