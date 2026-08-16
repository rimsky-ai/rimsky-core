---
audit: wait-set-topic-kind-taxonomy
artifact: decision:wait-set-topic-kind-taxonomy
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:40:04Z
---

# Four values, three of them the signal taxonomy's whole top level

Supported. One function maps a signal pattern to the wait-set discriminator, and it returns exactly four values: the three canonical kinds and a default. Those three are the complete top-level kind set the signal package declares — terminal, transient, attribute, and no fourth — so the projection is faithful rather than a chosen subset. Both persistence drivers constrain the column with a CHECK admitting exactly the same four strings, so an unmapped pattern lands on the fallback instead of failing the insert. Wait-set rows are written from three production sites, two through the mapping function and one with a canonical literal, so no row can carry a value outside the set; the one place the string message appears as a topic kind belongs to the template validator's substitution-coverage struct, a different type that never reaches the wait-set table. The discriminator participates in the row's primary key and is returned on the admin wait-set surface, though no index or query filters on it today.
