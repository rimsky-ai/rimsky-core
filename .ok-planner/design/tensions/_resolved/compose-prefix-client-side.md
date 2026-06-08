---
tension: compose-prefix-client-side
category: inconsistent
status: resolved
affects:
  - rimsky
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

- Make the reservation a server-enforced invariant: the control plane rejects compose-prefixed names submitted outside the privileged compose path, so the prefix is reserved at the source of truth rather than only by the client (see `concept:control-api`, `concept:tag`).
- Give compose-owned artifacts their own persisted namespace distinct from the general tag/instance-key space, so the prefix reservation is structural rather than conventional.
- Accept the convention as client-side-only and document loudly that the compose workflow assumes a single producer of compose-prefixed names.

## Evidence

- `_discover/rimsky-cli-compose-prefix-reservation.md` Description.
- `_discover/2026-05-10-rimsky-cli-thin-client.md` "compose prefix" para.
- CLAUDE.md "Non-obvious gotchas".

## Resolution

Resolved per spec:2026-06-06-comprehensive-gap-closure. The reservation is now a server-enforced invariant rather than a client-side convention: tag-create and instance-create at the control-api reject a `compose:`-prefixed name from any caller except the privileged compose path. The compose path identifies itself with a trusted compose-origin marker on its requests, which the guard checks before allowing a reserved-prefix write; the marker discriminates compose-originated writes from foreign ones rather than blocking the prefix unconditionally. The reservation now holds at the source of truth, so a non-CLI tool can no longer silently collide with the compose namespace. See the new server-enforced Invariant on `concept:control-api` and `concept:tag`.

