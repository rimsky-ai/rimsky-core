---
audit: inproc-utility-executor
artifact: story:inproc-utility-executor
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Utility node kinds dispatch with no executor service deployed

Supported. A zero-config `rimsky-all-in-one` container — no mounted config, no
executor block, no service containers — accepted a template referencing all
three bundled utility kinds and ran every one of them. The loop-counter emitted
its count, the send node put its message in the ledger, and the
attribute-passthrough receiver carried that message's body into its own output
attributes; each kind started once and settled `terminal/success`. The
deployment's executor list, read through the control API, resolves entirely to
in-process addresses, so no external executor service exists to have served
them. The population is the three kinds the runtime registers as bundled
utilities, and all three ran in one template.
