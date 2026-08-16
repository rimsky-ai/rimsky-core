---
experiment: named-lock-metric
commit: PENDING
---

# Named-lock acquisitions graph beside producer-claim acquisitions

## What it ran against

A `rimsky-all-in-one` stack from the tree's own image tag with its metrics
listener opened (`RIMSKY_METRICS_HOST`, `RIMSKY_METRICS_PORT`), a named lock of
limit 1, the bundled `rimsky-claim-producer-filesystem` service, and `peer/` —
the permissive-peer-build experiment's third-party executor, rebuilt for Linux
by the run — so each lock holder can be made slow enough that the others queue.
One instance runs three slow nodes contending for the lock and a fourth node
taking a producer claim. The metrics are then scraped from the supervisor role's
endpoint.

## What was observed

Nine checks, none failing. `rimsky_claim_acquisitions_total` came back as a
prometheus counter with help text, carrying both acquirer kinds as label values
on the one metric family: `acquirer_kind="named_lock",acquirer="gate"` at 3
acquisitions — one per holder — beside `acquirer_kind="producer",acquirer="files"`
at 1. Contention appeared as its own labelled series, `intent="unavailable"` on
the lock at 20, so saturation is a counter to graph rather than something to
reconstruct from events.
