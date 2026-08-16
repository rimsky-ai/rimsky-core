---
audit: cascade-flags-required-no-defaults
artifact: decision:cascade-flags-required-no-defaults
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:03:38Z
---

# The cascade-shape flag is required on every subscription entry, with no default applied

Supported. The flag is modelled as an optional pointer in the template spec precisely so absence is distinguishable from false, and two independent layers reject absence: template validation emits a registration error naming the field and stating that no default applies, and edge construction refuses to build a subscription edge from an entry lacking it. Registration rejects on that error — the deploy handler returns a validation failure whenever the result carries errors — so the claim that registration rejects entries missing the flag is real rather than advisory. Coverage of "every subscription entry" was checked by enumerating the surfaces where subscription entries exist: the node definition's subscribe list is the only one, and the validator walks it for every node in the flattened template, which includes subgraph nodes. No read site substitutes a default for a template-authored entry; the one place the flag is written without an author is the structurally injected root edge, which is a different construct governed by its own decision, and the value the validator offers inside an uncovered-substitution error is a suggested drop-in entry that spells the flag out explicitly rather than a default applied to anything. Both rejection paths carry tests.
