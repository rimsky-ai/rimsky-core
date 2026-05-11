---
tension: compose-prefix-client-side
category: inconsistent
status: open
affects:
  - rimsky-cli
  - template
  - tag
  - control-api
  - instance
---

# `compose:<project>:<...>` reservation is client-side only; server accepts manual registration

## What is muddy

CLAUDE.md says: "Compose owns project-prefixed names. Tags `compose:<project>:<...>` and instance keys `compose:<project>:<...>` are reserved for `rimsky-cli compose`. The CLI rejects manual registration with this prefix client-side."

But:

- The CLI checks the prefix client-side and rejects.
- The control-api server does NOT enforce the reservation. A `curl POST /templates` with a `compose:` tag is accepted.

The reservation is a convention, not an invariant. A non-CLI tool that uses the same prefix can collide silently.

## Why it matters

The `rimsky-cli compose` workflow relies on the prefix being predictable to scan/diff/teardown project artifacts. A third tool that produces a `compose:` tag would poison this workflow. The fix (server-side enforcement) is acknowledged but deferred.

## Resolution candidates (do NOT pick)

- Server-side CHECK constraint or chi middleware rejecting `compose:` outside a privileged endpoint.
- Promote the prefix to a separate `rimsky_compose_artifacts` table.
- Document the convention more loudly with a warning that the workflow assumes single-source-of-truth.

## Evidence

- `_discover/rimsky-cli-compose-prefix-reservation.md` Description.
- `_discover/2026-05-10-rimsky-cli-thin-client.md` "compose prefix" para.
- CLAUDE.md "Non-obvious gotchas".

