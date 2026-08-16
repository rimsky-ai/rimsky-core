---
experiment: assumption-event-kinds-one-naming-scheme
commit: PENDING
---

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
