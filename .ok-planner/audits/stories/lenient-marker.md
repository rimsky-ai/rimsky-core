---
audit: lenient-marker
artifact: story:lenient-marker
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:39:46Z
---

# A lenient substitution resolves a missing source to empty instead of failing the dispatch

Supported: a run through the control API of an all-in-one deployment registered
two templates identical but for the lenient marker on one substitution directive.
Each declares an optional upstream whose only subscription is to a message type
nothing sends, so the receiver's read of it is a missing source at dispatch time.
In both runs that upstream never ran. Without the marker the receiver settled on
a template-resolution error naming the directive it could not resolve. With the
marker the same receiver dispatched and settled successfully, its resolved bag
carrying the marked property as the empty string and its own unrelated property
at the declared value, so the leniency is scoped to the marked directive rather
than the whole bag. Seven checks, none failing.
