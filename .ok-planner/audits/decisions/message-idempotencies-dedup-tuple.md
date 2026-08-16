---
audit: message-idempotencies-dedup-tuple
artifact: decision:message-idempotencies-dedup-tuple
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:52:00Z
---

# Message dedup keyed on the full five-part sender tuple

Supported. Both storage dialects declare the idempotency ledger with a primary key over exactly the five columns the decision names — instance, sender kind, sender, sender subject, and idempotency key — in that order and with no additional or missing member, and the insert-or-lookup statement's conflict target is that same five-part key, returning the original message identity and an inserted flag rather than overwriting. The row type carried across the interface has the same five fields, and both production callers — the control-API message post and the in-graph send-message node — populate all five. The shared driver-parity suite proves the discrimination axis by axis against both drivers: a replay under an identical tuple returns the original identity and reports not-inserted, while changing the sender subject, the sender kind, the sender, or the instance each inserts a new row, which is precisely the collision the rejected instance-plus-key alternative would have allowed. A concurrent-insert case covers the race on the same tuple.
