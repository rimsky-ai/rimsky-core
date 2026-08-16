---
audit: fanout-list-array
artifact: story:fanout-list-array
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:39:46Z
---

# A fan-out over a list an upstream node produced runs one work unit per item

Supported: a run through the control API of an all-in-one deployment drove a
template whose first node writes a three-element list as its own attribute and
whose second node names that attribute as its partition request over a claim on
the bundled filesystem producer. The only producer in play is the one the image
ships — the run supplies none of its own, and the container log confirms the
bundled one registered. The producer split the parent claim into three
sub-scopes, the fan-out dispatched three work units keyed by the three items the
list declared, each work unit resolved its own key into its attribute bag, and
the parent plus its three work units all settled fresh with no failures. Six
checks, none failing.
