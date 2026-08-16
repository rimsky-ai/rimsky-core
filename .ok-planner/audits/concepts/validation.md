---
audit: validation
artifact: concept:validation
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:24:31Z
---

# The validation mix-in protocol, its registration-time pipeline, and its role advertisement

Unsupported, on one invariant. Most of the concept holds: the protocol carries exactly one method whose request pairs a role string with a four-way choice of role-specific context, and whose response is a validity flag beside error and warning collections; the executor context carries the node alias, the claim aliases, and an attribute schema the pipeline builds by merging the executor's advertised expectations, the template-level defaults, and the per-node declaration. The registration handler runs the pure static template check first — including the expected-attributes comparison against that merged schema — rejects on its errors, then runs the validate RPCs and rejects on theirs, surfacing warnings from both steps to the caller; the RPC fan-out is per-node for the executor and claim-producer roles and template-wide for the publisher and lifecycle-subscriber roles, with every registered validator advertising the subscriber role consulted since no template names one. Unreachable validators produce a warning by default and an error under an operator-set deployment-level strict mode. What fails is the invariant that a validation-supporting service's capabilities advertise the role discriminators it will validate: that holds for the three peer kinds dialed from the producer, executor, and publisher configuration blocks — all three capability surfaces carry the role list and all three are fetched — but a service configured through the standalone validators block never has its capabilities consulted at all; its role set is taken from its declared configuration protocols, and one declaring only the validation protocol registers with an empty role set and is silently never called.
