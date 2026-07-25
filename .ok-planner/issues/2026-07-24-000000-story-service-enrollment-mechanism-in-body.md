---
issue: story-service-enrollment-mechanism-in-body
kind: audit
category: muddy-boundary
artifacts:
  - story:service-enrollment
status: verified
opened: 2026-07-24T00:00:00Z
---

# A user-promise document quotes the route, the TTL, and the certificate grammar — all of which already live elsewhere

Rimsky's "story" documents record user expectations; implementation specifics (exact routes, timeouts, wire formats) belong in "decision" documents. The service-enrollment story — how a standing service like a sensor or executor obtains credentials to talk to rimsky — quotes all three in its capability text: the exact HTTP enrollment route, the certificate's 24-hour lifetime, and the exact identity format embedded in the certificate (a SPIFFE-style URI carrying the calling API key's ID). Every one of those specifics is already documented in existing decisions, so this is pure duplication — the kind that silently falsifies the story when a pre-v1 rename moves the route or lifetime.

One wrinkle keeps this from being a mindless strip: the identity-format fact is load-bearing for the story's Falsifier ("revoking a key stops future certificates" is only checkable because the certificate ties back to the requesting key). But the *property* survives without the *grammar* — "the certificate's identity binds to the calling key" says everything the falsifier needs, without spelling the URI format. A broader sibling issue covers the same restating-mechanism pattern across several other stories, so this file's fate probably rides with that sweep.

## Options

- **Full strip-to-citation**: capability describes obtain / auto-renew / revoke-stops-issuance; route, TTL, and grammar drop; the falsifier restates at identity-binds-to-key altitude. Cheapest rewrite in the batch — nothing needs a new home.
- **Partial strip**: keep the identity-binding sentence with its format, drop only the route and TTL.
- **Leave as-is** pending the corpus-wide sweep's ruling — consistent with siblings that do the same, and defers rather than decides.

The ruling decides how much strips, and whether this folds into the corpus-wide stories sweep.

## Ruling

> Recommended ruling (/recommend-rulings): Full strip-to-citation: the
> Capability describes obtain/auto-renew/revoke-stops-issuance as
> user-observable outcomes; route, TTL, and SAN grammar drop (the
> decisions already carry them), with the Falsifier restated at the
> identity-binds-to-the-calling-key altitude. Fold into the corpus-
> wide stories sweep.
>
> Rationale: Every stripped detail already has a decision home, so
> this is the cheapest rewrite in the batch — and the falsifier's
> substance survives without the wire grammar.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
