---
audit: claim-co-holdership
artifact: concept:claim-co-holdership
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:46:57Z
---

# Co-holdership: the directive, its registration rules, and its six invariants

Supported. All three registration rules the concept states are enforced by the template validator and each has a test: the pointer must name a node declared in the template, may not name the co-holder's own node type, and the graph formed across the template's co-hold declarations is walked for cycles and rejected on one. The chain prohibition is real in both places the concept claims: the registration validator requires the named alias to appear in the pointed-to node's own claim declarations, and the runtime resolver independently looks the alias up in that node's own claim declarations and yields nothing when it is absent, so a co-holder cannot be pointed at through another co-holder. The optional rename resolves through one shared local-alias function used by the validator, the collision check, and the runtime, and duplicate local aliases are rejected at registration. Co-holder rows are inserted in the co-holder's own acquire transaction keyed by the holder run. The dispatch handle built for a co-held claim carries exactly alias, address, and payload — three fields, against the fresh acquirer's handle which additionally carries producer kind, intent, and the producer candidate handle — and the substitution context carries address, payload, and claim scope. Alias collision resolves acquisition-first in both places the concept says it must: the substitution claim map and the executor wire payload each skip the co-held entry when the alias is already occupied by an opened claim. Auto-terminal returns without firing while any co-holder row is active, and the poison rule is implemented at the settling holder's release: a failed held settlement marks every still-active co-holder row failed and only then runs the resolution check, so abandon fires at that moment rather than at the last holder's settle, and holders still in flight take the poisoned terminal path when they settle. Six of the auto-terminal tests plus four scenario suites exercise these paths.
