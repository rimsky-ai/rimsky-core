---
audit: cascade-flags-on-subscribes
artifact: decision:cascade-flags-on-subscribes
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:41:50Z
---

# Whether the cascade-behavior flag lives on the subscription entry and nowhere else

Supported, including the two negatives. The upstream-refresh flag is a field of the subscription-entry struct, carried in both its YAML and JSON forms, and the derived subscription edge carries it forward. The substitution-ref struct has six fields and none is a cascade flag, so the per-ref alternative is not present; the separate block the decision rejects appears nowhere in the tree at all — grepping for it across code, protos, and documentation finds it only inside the design corpus, in this decision's own Alternatives and an archived planning record. The "currently" qualifier is accurate: the second flag an earlier design carried is gone from the code entirely, leaving exactly one cascade-behavior flag, on the subscription entry. Enforcement goes further than the decision claims — the flag is required rather than defaulted, and the validator rejects a template whose entries disagree about it for one sender, which is the "which value wins?" ambiguity the rationale names, refused at registration. Every read site in the graph and validator layers reads it off a subscription entry or the edge derived from one, and roughly ten end-to-end scenario tests exercise the flag's cascade behavior.
