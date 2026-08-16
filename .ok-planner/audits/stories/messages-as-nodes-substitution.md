---
audit: messages-as-nodes-substitution
artifact: story:messages-as-nodes-substitution
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:07:07Z
---

# One substitution channel: the messages directive resolving like the nodes directive

Supported: the two directives resolve through one lookup and one validation
regime. A single node sourced one attribute through each form and settled with
both values in the same dispatch, and the value the messages form read appears as
an ordinary attribute write on the message type's own node — which is what makes
"the same lookup" a demonstrated fact rather than a claim, since the declared
message type is materialised as an ordinary node of the instance. The
one-channel-to-learn benefit was checked where it is most likely to leak, at
registration: five templates were registered, and the namespaces stay apart in
both directions — a messages form naming an undeclared message type is refused,
a nodes form naming a message type is refused as an unknown node, and an
uncovered reference of either form draws the same coverage finding naming the
subscribes entry to add. One asymmetry the run also records: the two namespaces
do not converge on the subscription side, where an attribute-changed edge on a
message type is refused because delivery only ever manifests as a success
terminal. The interchangeability itself was taken in the attribute-source
context; the run does not enumerate every context a nodes directive is legal in.
Eight checks, none failing.

## Compliance

- The body quotes the template-language directives literally, which is the surface a template author reaches through and belongs in decisions; the compliant text names the need, e.g. "I can read a message body wherever I can read a node's attribute, through one substitution channel".
- The clause after the em-dash states the enforcement mechanism and its timing ("registration-time validation enforces …") rather than a user need; the need is that a mistyped reference is refused before the template is usable, and the mechanism belongs in a decision.
