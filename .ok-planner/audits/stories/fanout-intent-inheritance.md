---
audit: fanout-intent-inheritance
artifact: story:fanout-intent-inheritance
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:24:46Z
---

# Sub-claims opened by a fan-out carry the intent the template declared

Supported: a run through the control API of an all-in-one deployment, with the
bundled filesystem claim producer over a throwaway workspace, drove the same
fan-out shape under each of the two claim intents. Under the read-only intent the
run opened one parent handle and three sub-handles, every sub-handle pointing at
that parent and every one carrying the read-only intent, and all four acquisitions
the run recorded named that intent and no other. Under the read-write intent the
same shape produced a parent and three sub-handles all carrying read-write, so
what the sub-claims carry tracks the template's declaration rather than a fixed
producer default. Eight checks, none failing.

## Compliance

Prescribes mechanism by spelling the declaration as a literal template key and enum value; the compliant text says the author declares a fan-out claim read-only and trusts every sub-claim to inherit that.
