---
audit: named-lock-metric
artifact: story:named-lock-metric
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:48:35Z
---

# Named-lock acquisitions are a graphable counter beside producer-claim acquisitions

Supported. Driven through the public surface against a released-image stack with
its metrics listener opened, a named lock of limit one, the bundled filesystem
claim producer, and a third-party executor rebuilt for Linux by the run so lock
holders are slow enough that the others queue; one instance ran three nodes
contending for the lock and a fourth taking a producer claim. Nine checks, none
failing. The scrape returned one prometheus counter family with help text
carrying both acquirer kinds as label values — the named lock at three
acquisitions, one per holder, beside the producer at one — with the lock named on
its own series, so the two live on the same metric rather than in separate
places. Contention came back as its own labelled series at twenty, which is the
saturation signal the story asks to graph and alert on rather than reconstruct
from events.
