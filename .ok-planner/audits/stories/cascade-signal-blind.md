---
audit: cascade-signal-blind
artifact: story:cascade-signal-blind
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:39:31Z
checked: 3
unaccounted: 0
---

# Every cascade-firing signal kind is reachable through one uniform subscription form

Supported: the signal taxonomy carries exactly three cascade-firing kinds —
success terminals, error-class terminals, and per-key attribute-changed signals
— with the transient family audit-only and non-firing. A run through the
control API of an all-in-one deployment registered a template emitting all
three, with four receivers each declaring one subscription entry: an exact
success terminal, an attribute-changed path, an error wildcard, and the same
wildcard under a CEL predicate. All four receivers dispatched exactly once, and
every entry used the same declaration keys plus the optional predicate key, so
no kind needed a special form. A second template subscribing to a transient park
was refused at registration by the same mechanism, with the error naming the
signal and stating transient signals are audit-only. Seven checks, none failing.
