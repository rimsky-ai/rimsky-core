---
issue: retry-backoff-values-unvalidated
kind: audit
category: config-surface
artifacts:
  - concept:error-policy
status: promoted
opened: 2026-08-06T06:49:18Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# Unknown retry-backoff values register cleanly and silently behave as flat delay

A template's `retry_backoff.kind` and `.jitter` accept any string. No
validator inspects them (zero references in the template validators),
so `kind: quadratic` — or any typo — registers and deploys clean, then
hits the delay computation's default arm
(`lib/graph/node/backoff.go:22-31`) and behaves as flat backoff;
likewise an unrecognized jitter value silently applies no jitter. The
misconfiguration is discoverable only by watching retry timing in
production. The sibling vocabulary on the same policy surface,
`error_types[*].action`, is range-checked at registration — this pair
simply never got the same gate. The fields are already typed as the
named `BackoffKind`/`JitterKind`, so this is purely a missing
validation, and the fix shape is fully determined by the existing
pattern: reject unknown values at registration with the standard
validation-error shape.

What keeps this out of an in-place repair is that the fix is a
behavior change — templates that register today would be rejected
tomorrow — which makes it a new invariant the corpus must carry, an
intent-level addition only a sprint may make. The error-policy concept
documents the `action` vocabulary's enforcement and says nothing about
these two fields.

## Options

Effectively one: add registration-time validation of both enums,
mirroring the `action` enforcement. The only variance is hard error
versus warning, and where the corpus records the new invariant.

The ruling adopts the forced fix and fixes its shape.

## Ruling

> Generated ruling (/verify-issues): validate `retry_backoff.kind`
> and `.jitter` against their closed value sets at template
> registration, rejecting unknown values as hard errors exactly like
> the neighboring `error_types[*].action` check — forced by the
> project's one-idiom rule (the same policy surface already enforces
> its vocabularies) and its silent-misconfiguration discipline. The
> new invariant lands in the error-policy concept alongside the
> `action` one.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
