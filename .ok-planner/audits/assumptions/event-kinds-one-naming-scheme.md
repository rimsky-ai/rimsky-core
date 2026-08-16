---
assumption: event-kinds-one-naming-scheme
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# event kinds follow one naming scheme, so prefix filtering works predictably — everything claim-related starts with `claim`, everything auth-related with `auth.`.

As operator filtering the event feed, I would take it that event kinds follow one naming scheme, so prefix filtering works predictably — everything claim-related starts with `claim`, everything auth-related with `auth.`.

## Source

sibling-symmetry — `claim_acquired` and `claim_resolved` beside `claim_resolution.commit` and `subclaim.acquired`, mixing separators

## What a run would observe

filter `GET /v1/events` by kind prefix `claim` and by `subclaim` and check which of the eight claim-ish kinds each returns.

## Measured

The experiment `assumption-event-kinds-one-naming-scheme` filtered
`GET /v1/events` by prefix and by exact kind, and drove a fan-out and a
claim-and-lock node. Prefix filtering does not exist: `kind` is an exact match,
and `claim`, `subclaim`, `auth.`, `claim_` and `claim*` were each rejected with
HTTP 400. `claim/` was the exception and the worse case — it parses as a signal
type-path, returns HTTP 200, and matches nothing, so the operator's prefix
guess reads as an empty feed rather than an error. The vocabulary the rejection
prints — 44 operational kinds plus `terminal/*`, `transient/*` and
`attribute/*/changed` — carries three separator conventions at once: snake_case
`claim_acquired`, dot-separated `subclaim.acquired`, and slash paths. Nine
claim-ish kinds are valid under three different first words (`claim_`,
`subclaim.`, `orphaned_claim_`), so no lead token reaches them even if prefixes
worked. One family is spelled both ways in the same vocabulary:
`fan_out_dispatched` and `fanout.children_created` are both kinds while
`fanout_children_created` and `fan_out.dispatched` are both rejected, and a
single fan-out timeline carried both spellings side by side. The prior's one
correct half is `auth.` — every auth kind is dotted.
