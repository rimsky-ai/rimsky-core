---
story: cascade-defers-during-flight
---

# In-flight node-runs are sealed against cascade-driven invalidation

## Story

As a template author whose executor relies on a fixed view of its inputs across the dispatch arc, I can rely on an in-flight node-run being sealed against upstream events — an upstream cascade during my run produces a new queued run that dispatches after mine settles, and a parked run is woken early rather than rewritten — so that one dispatch means one set of inputs in and one outcome out, with no retroactive mutation underneath a running executor.
