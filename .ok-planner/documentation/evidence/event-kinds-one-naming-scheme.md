---
trap: event-kinds-one-naming-scheme
release: d977250c
---
# Evidence set — event kinds follow one naming scheme, so prefix filtering works predictably — everything claim-related starts with `claim`, everything auth-related with `auth.`.

Source of the prior: sibling-symmetry — `claim_acquired` and `claim_resolved` beside `claim_resolution.commit` and `subclaim.acquired`, mixing separators

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-event-kinds-one-naming-scheme)

# Filtering the event feed by prefix, and reading the vocabulary back

## What it ran against

A `rimsky-all-in-one` stack with a `rimsky-claim-producer-filesystem` and one
named lock. The run asks `GET /v1/events` for prefixes and for exact kind
names, reads the vocabulary the API prints when it rejects one, then drives a
fan-out and a claim-and-lock node and lists the kinds each timeline carries.

## What was observed

The `kind` parameter is an exact match, not a prefix. `kind=claim`,
`kind=subclaim`, `kind=auth.`, `kind=claim_` and `kind=claim*` were each
rejected with HTTP 400. `kind=claim/` was accepted with HTTP 200 — it parses as
a signal type-path — and matched nothing, so a prefix guess in that shape
returns an empty feed rather than an error.

The rejection prints the whole accepted vocabulary: 44 operational kinds plus
`terminal/*`, `transient/*` and `attribute/*/changed`.

Nine claim-ish kinds are all valid and sit under three different first words —
`claim_acquired`, `claim_held`, `claim_resolved`, `claim_resolution.abandon`,
`claim_resolution.commit`, `subclaim.acquired`, `subclaim.begin_candidate`,
`orphaned_claim_lost_race`, `orphaned_claim_released` — so no single lead token
reaches them even if prefixes worked.

One vocabulary carries three separator conventions: snake_case
(`claim_acquired`), dot-separated (`subclaim.acquired`), and slash paths
(`terminal/*`, `transient/*`, `attribute/*/changed`). One family is spelled
both ways at once: `fan_out_dispatched` and `fanout.children_created` are both
kinds, while `fanout_children_created` and `fan_out.dispatched` are both
rejected — and a single fan-out timeline carried both spellings side by side,
next to `subclaim.acquired` and `lock_acquired`.

Runnables: `src:.ok-planner/experiments/assumption-event-kinds-one-naming-scheme/` at the stamped commit.
