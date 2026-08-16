---
audit: substitution-grammar-closed
artifact: decision:substitution-grammar-closed
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:29Z
---

# The grammar is six data-reference kinds and no cascade-shape token

Supported. Directive resolution is one switch over the leading token with a closed set of six kinds — claim, params, nodes, messages, child, env — and a default that returns a missing-source error naming the unknown kind; the template validator carries the same closed set, rejecting any directive whose prefix is outside it with an error listing the six. Every one of the six resolves a value: an acquired claim's fields, an instance parameter, a sender node's attribute, a delivered message body, a child result, or a host environment variable. None declares or shapes a cascade edge, and the edge builder reads nothing from the directive grammar — cascade shape comes from the subscribes entry's type pattern, predicate, and upstream-refresh flag, which is the separate surface the decision points at. The one place a directive could plausibly have carried topology, the fan-out partition request, resolves through the same closed grammar, with tests binding it from a node attribute and from an acquired claim.
