---
audit: sub-claim-payload-substitution
artifact: story:sub-claim-payload-substitution
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:39:46Z
---

# Per-sub-claim payload reads resolve the same way as on a regular claim

Supported: a run through the control API of an all-in-one deployment, with the
bundled filesystem producer declaring pick policies that supply a payload per
claimed item, drove two nodes carrying byte-identical attribute sources — one
field path and one whole-payload path — differing only in how the claim arrives.
On a directly opened claim the field path resolved to that claim's payload field
and the bare path to the whole payload object. In the fan-out, three sub-claims
were opened and each clone settled carrying the payload of its own sub-claim: the
resolved field values equalled the set of sub-claim partition keys, no two clones
resolved the same value, and the bare path returned the same object whose field
the field path had returned. The resolved attribute shape was identical in both
contexts, and the parent and all three clones settled fresh. Seven checks, none
failing.

## Compliance

Prescribes mechanism by quoting the substitution directive's literal grammar; the compliant text says the author reads per-sub-claim data with the same claim-payload directive a regular claim takes.
Prescribes mechanism by naming the producer protocol's acquisition verb; the compliant text says a regular claim the node holds.
