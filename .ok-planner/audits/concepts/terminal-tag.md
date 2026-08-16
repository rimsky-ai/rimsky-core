---
audit: terminal-tag
artifact: concept:terminal-tag
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:42:19Z
---

# Executor-declared tags: set semantics at decode, two declared-tag gates, and an ephemeral lifecycle

Supported. Tags are deduplicated at the wire-decode boundary for all three outcome kinds the concept names — success, error, and park — through one shared helper, so set semantics are established before the verdict goes anywhere. Both gates exist. At template registration, tag literals are extracted from a subscription's payload-tags filter and each is checked against the sender node's effective executor's declared-tag set, with an undeclared reference recorded as a validation error that refuses the registration. At runtime, immediately as the wire outcome is decoded into the internal settling verdict and before it reaches terminal dispatch, every tag the verdict carries is checked against the same declared set, and any undeclared one replaces the whole verdict with an error terminal whose class is the protocol-violation class, carrying the offending tag and the declared list. Both gates fall through when the executor has advertised no declared-tag set at all, which is consistent with the concept's own statement that the executor's observability declaration is the registry. The payload-shape claims hold against the declared proto messages: tags ride both run-terminating payloads alongside the attributes-delta slot, and the park payload carries tags but no attributes-delta, while park subscriptions are rejected outright by the subscription validator, so the discriminator role reaches subscribers only through the eventual run-terminating settlement. The ephemerality claim — never merged into the per-run attribute ledger, never carried forward — is exercised end to end by a scenario test named for it, and the wire round-trip is covered by an executor conformance scenario.
