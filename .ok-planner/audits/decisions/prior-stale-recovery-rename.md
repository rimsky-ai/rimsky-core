---
audit: prior-stale-recovery-rename
artifact: decision:prior-stale-recovery-rename
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095809-stale-recovery-disposition-sync-path-unrealized
---

# Stale-recovery prior-dispatch disposition

Unsupported. The disposition value is stamped by exactly one production code path, the asynchronous quiet-period/max-runtime sweep — checked against every call site that stamps a prior-dispatch disposition in the module. Every synchronous dispatch error path, including RPC dial failure, timeout, and cancellation, unconditionally stamps the sibling retry disposition instead, regardless of error class. The claim that this value covers both the asynchronous case and the synchronous-RPC-broken case does not hold; only the asynchronous half is realized in code, consistent with a separate concept document's narrower description of the same value.
