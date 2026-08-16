---
audit: tag-based-subscription
artifact: decision:tag-based-subscription
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:38:09Z
---

# Named-event subscription is a terminal type plus a CEL tag filter, with no event leaf in the taxonomy

Supported. The signal taxonomy declares eight canonical emit patterns — two terminal, five transient, one attribute — and none is an event leaf; subscription types are derived from that same list, so an event type-path cannot be declared or subscribed to, and a template using the retired event directive form is rejected at validation. The tag set travels as a repeated string field on the terminal success and terminal error payload messages, so a filter expression reads it off the payload the subscription matches: the CEL environment exposes the payload as a string-keyed map of dynamic values, and for a concrete terminal type the compiler additionally checks every payload field name the expression selects against that message's declared fields, which include the tag field. Registration further checks each tag literal appearing in a filter against the sender executor's declared vocabulary and rejects one the sender never advertises. End-to-end scenarios subscribe two different downstream nodes to the same sender's success terminal on differing tag membership predicates and confirm each fires only on its own tag.
