---
audit: idempotent-mode-dedupes
artifact: story:idempotent-mode-dedupes
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:39:31Z
---

# Both idempotent cascade modes drop re-runs whose inputs equal a predecessor

Supported: a run through the control API of an all-in-one deployment drove a
sender that cascades to itself four times and a receiver cascaded once per
round, counting how often the receiver's executor was actually reached. Under
the non-idempotent control all four rounds reached the executor carrying a
byte-identical input bag. Under each of the two idempotent modes the story names
— comparison against the queued predecessor, and comparison also against the
most recent settled run — the same four rounds produced exactly one dispatch,
the three re-runs with identical inputs never reaching the executor. Re-run with
a receiver whose inputs change every round, both modes dispatched all four
rounds with four distinct bags, so the drop follows from input equality and not
from rounds being coalesced. Eight checks, none failing.
