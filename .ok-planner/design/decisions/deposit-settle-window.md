---
decision: deposit-settle-window
---

# Mid-write deposits are held by an mtime-quiescence settle window

## Choice

A per-watch, optional settle window: during a poll, any object whose modification time is within the window of the poll's clock is held — not published, and not marked seen, so later polls reconsider it and publish once it has been quiet for the full window, with metadata computed from the settled content. Applied in the sensor's poll loop, backend-agnostically. Default off.

## Rationale

Deposits that arrive by non-atomic means — cross-filesystem moves, in-place network copies — are visible mid-write, and consuming one violates the story's promise that content still being written is never treated as work. Modification-time quiescence is a technology-neutral completeness signal already present in every lister's metadata, so the guard lives once at the sensor layer instead of per backend. Default off because atomic deposit paths (object-store uploads, same-filesystem renames) do not need it, and the watch that does need it knows it does.

## Alternatives

- Two-poll etag confirmation — a stronger stability signal, but it doubles detection latency for every object and needs per-object candidate state; mtime quiescence catches the same writers with less machinery.
- Requiring atomic deposits by convention (rename-into-place only) — adopted as guidance for producers under our control, but unenforceable against network-copy arrivals, so it cannot be the only line of defense.
- Sidecar completion markers (a done-file per deposit) — pushes a protocol onto every producer, when the story's entire point is that producers just drop content.
