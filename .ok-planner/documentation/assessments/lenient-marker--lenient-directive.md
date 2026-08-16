---
assessment: lenient-marker--lenient-directive
subject: story:lenient-marker
way: lenient-directive
release: d977250c
outcome: held
warrant: experiment:lenient-marker
---
# Marking one substitution directive lenient so a missing source resolves to empty

Two templates identical but for the lenient marker on one substitution directive were registered and run. Each declares an optional upstream whose only subscription is to a message type nothing sends, so the receiver's read of it is a missing source at dispatch time, and in both runs that upstream never ran. Without the marker the receiver settled on a template-resolution error naming the directive it could not resolve. With the marker the same receiver dispatched and settled successfully, its resolved bag carrying the marked property as the empty string and its own unrelated property at the declared value — so the leniency is scoped to the marked directive rather than loosening the whole bag. A template author can therefore write a node that tolerates an optional upstream without giving up resolution errors everywhere else. Seven checks, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
