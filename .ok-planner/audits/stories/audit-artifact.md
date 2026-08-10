---
audit: audit-artifact
artifact: story:audit-artifact
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# A finished one-shot run leaves a record the operator can open later

Supported, in both of the CLI's one-shot modes. Each drove a mixed roster — one
leg succeeding, one failing — to terminal in the invocation that started it, and
each left a per-run artifact directory holding the run's state, its blob store
and the config it used, with the executor process gone before anything was read.
Serving a copy of that state back through a stack, the record answered the
ordinary read surface: both instances present and terminal, the event stream
replaying the success terminal and the failure's own error class, the instance,
its nodes, its events and a single node readable by verb, and the successful
leg's attribute writeback intact. Two consecutive reads returned identical
counts, so inspecting the record neither re-runs nor disturbs it.
