---
audit: template-subscriptions
artifact: story:template-subscriptions
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# Subscriptions fire only on a matching type-path whose payload satisfies the predicate

Supported. Against a zero-config all-in-one deployment, one source node emitted a
single terminal-success signal carrying its executor's attribute delta, and all 5
subscription forms declared against it behaved as the story promises: the exact
type-path fired, the trailing-wildcard prefix fired, the predicate the payload
satisfies fired, the entry on a different type-path did not fire, and the
predicate the payload fails did not fire. All 5 forms were admitted at
registration, and the 2 non-firing nodes ended the run with zero node runs while
the 3 firing nodes each ran once.

## Compliance

The capability clause names CEL, a specific expression-language choice with
identifiable alternatives, which the story rules place in decision territory —
and which no other story in the catalog names; the compliant text is "plus an
optional predicate over the signal payload".
