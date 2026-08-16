---
audit: held-abandon-cascades-abandoned
artifact: story:held-abandon-cascades-abandoned
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:27Z
---

# A downstream subscriber learning that held work was rolled back

Supported. A stack from this tree ran a template whose acquirer opens a claim on
the bundled filesystem producer, whose co-holder fails its work, and which
carries three subscribers outside the holding subgraph. Both ways the story names
were taken. The failure rolled the claim back with a single abandon, and the
acquirer emitted exactly one terminal signal, the abandoned error. The subscriber
naming that signal exactly and the subscriber using the broader error-family
pattern each ran, each starting at a sequence number after the abandon, so each
learned of the rollback at the moment it happened. The subscriber on success
never ran, so a rollback is never reported to downstream as a success.
