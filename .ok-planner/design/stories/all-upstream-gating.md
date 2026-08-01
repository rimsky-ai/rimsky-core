---
story: all-upstream-gating
status: as-is
---

# Template author relies on all-upstream gating for fan-in shapes

## Story

As a template author building a fan-in shape (a node subscribing to several upstream siblings), I can rely on the receiver dispatching only after all of its in-flight upstreams in the frame have resolved — regardless of how their staleness arrived — so that the receiver never runs against a half-settled upstream set.
