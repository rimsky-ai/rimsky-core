---
audit: no-event-substitution
artifact: decision:no-event-substitution
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:44Z
---

# The substitution grammar carries no per-emission event source kind

Supported. The directive resolver dispatches on the leading source kind through a closed switch with exactly six arms — claim, params, nodes, messages, child, env — and any other leading token falls to a default arm that returns a missing-source error naming the unknown kind; there is no event or events arm anywhere in the grammar. A unit test pins the six live arms by name and fails if any of them routes to the default, and a second case asserts an unrecognised kind is rejected as a missing source. Per-emission data reaches downstream readers through the Success attributes delta, which lands in the node's attribute bag and is read by the node-attribute directive form, so no second channel carries the same bytes. No named-event ledger exists for such a path to read from — the event log stores terminal and signal records and is not a substitution source.
