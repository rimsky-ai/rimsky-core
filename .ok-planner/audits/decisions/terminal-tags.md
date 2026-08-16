---
audit: terminal-tags
artifact: decision:terminal-tags
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:38:09Z
---

# Terminals carry a deduplicated tag set; no named-event message and no streaming variant exist

Supported. All three settling outcome messages — success, error, park — declare a repeated string tag field, and the supervisor deduplicates each one as it decodes the outcome, on the synchronous and the async-callback paths alike; the transient await-async hand-off carries none. No named-event message exists in any proto in the tree, and the executor service declares a single unary call, so there is no streaming variant to carry one. The executor observability capability declares the emit vocabulary as a repeated declared-tags field, the registration gate resolves it per sender executor and rejects a subscription filter naming a tag the sender never advertises, and the supervisor independently rejects a terminal carrying an undeclared tag as a protocol violation. Per-emission data rides the attributes delta, which success and error both carry. Checked all four outcome variants, the three signal payload messages that carry tags, and both validation points.

## Compliance

Rationale narrates a retired mechanism in the past tense ("NamedEvent's runtime semantics were batch-at-terminal — the runner captured events during the stream but processed them only at terminal time"), which is historical content; the compliant text states the present property instead, e.g. "A terminal's tag set is fixed at settle, so tags are terminal-level metadata rather than a per-emission stream."
Rationale enumerates what a change deleted ("removing the parallel ledger, the event/<name> signal-taxonomy leaf, the substitution path, and the per-event audit emit"), an audit-trail line whose subject is what changed; the compliant text asserts the current state, e.g. "There is no per-emission ledger, no event leaf in the signal taxonomy, no event substitution path, and no per-event audit emit."
