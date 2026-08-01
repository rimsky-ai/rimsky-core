---
story: multi-hard-dep-rendezvous
status: as-is
---

# Template author relies on multi-hard-dep rendezvous

## Story

As a template author declaring two or more force-refreshed upstreams on one node (`concept:cascade`), I can rely on each upstream running once and the receiver dispatching once with all of them settled, so that the shape rendezvouses — computing from the full refreshed set — instead of livelocking or re-running upstreams.
