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

Resolved per spec:2026-06-06-comprehensive-gap-closure. The reservation is now a server-enforced invariant rather than a client-side convention: tag-create and instance-create at the control-api reject a `compose:`-prefixed name from any caller except the privileged compose path. The compose path identifies itself with two independent signals — a per-request compose-origin marker stamped on the HTTP header AND a `compose:origin` capability action that the caller's api-key grant must match — both of which must be present for the guard to allow a reserved-prefix write. The header alone (e.g. a raw `curl` from any authenticated caller holding `tag:create` or `instance:create`) is not a trust boundary; the load-bearing check is the `compose:origin` permission. A caller holds `compose:origin` whenever their grant matches the action through the same wildcard rules every other action uses (see `concept:permission` "Action grammar"), which includes any `*` (admin) or `*:origin` wildcard grant — so the `admin` role-template holds `compose:origin` by virtue of holding everything, intentionally. Non-wildcard non-compose-CLI keys do NOT hold `compose:origin`: a key that mints `tag:create` and `instance:create` only is rejected on a reserved-prefix write, which is the load-bearing isolation. The reservation now holds at the source of truth, so neither a non-CLI tool nor a non-admin authenticated caller spamming the marker can silently collide with the compose namespace. See the server-enforced Invariant on `concept:control-api`, `concept:tag`, and `concept:permission` (the `compose:origin` capability action).

