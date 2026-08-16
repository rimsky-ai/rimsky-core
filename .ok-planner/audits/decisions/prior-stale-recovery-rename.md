---
audit: prior-stale-recovery-rename
artifact: decision:prior-stale-recovery-rename
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:48:37Z
---

# One stale-recovery disposition across both async deadlines, distinct from the sync retry disposition

Supported. The disposition vocabulary is a four-value protocol enum — none, stale recovery, retry after error, and recalculate — carrying exactly one stale-recovery member, with no per-deadline split, and the runtime maps the storage spelling onto it through a single translation function. The deadline sweep is the sole producer of that value: it walks the orphaned-claim candidates, decides between the two async deadlines by comparing the dispatch's runtime against its effective maximum and its quiet window against the last progress timestamp, and then releases the claim under the same stale-recovery disposition whichever of the two fired — the distinction survives only in the error class the sweep records on the event, never in the disposition. The sync side is genuinely distinct and stamps the other value at both of its exits: the in-place retry path stamps retry-after-error inside the settlement transaction, and the release-and-requeue path releases the claim under the same value after commit; the infra terminal that catches dial, resolve, and cancellation failures routes through that same stamp. The bundled claude-agent executor decodes both wire spellings back to its own disposition constants, and the cross-driver conformance suite asserts both values round-trip through release, stamping, and candidate read on each persistence driver.
