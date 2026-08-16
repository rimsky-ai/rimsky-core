---
issue: cli-api-key-clause-not-universal
kind: audit
category: conflicting
artifacts:
  - concept:rimsky
status: verified
opened: 2026-08-16T09:09:00Z
---

# The rimsky concept says every CLI verb accepts an api-key flag and sends it; six define none and the compose family drops it

The rimsky concept's api-key invariant says every verb accepts an api-key flag, falls back to the environment variable, and sends the key as the bearer token. Two things fail. The compose verbs (up, down, plan, status) parse the key flag and then build their client without ever attaching it — an operator supplying a valid key gets a 401 that reads as a server fault. And six of 26 verbs define no key flag at all (context current, agent status, agent start, compose run, and two conformance verbs) — plausibly because each either dials no control API or authenticates by a different credential, but the concept cannot tell a reader which. The ruling decides whether the universal is made true or restated as classes; the compose credential drop is a bug under either.

## Options

- Make the universal true — every control-API-dialing verb takes the general key flag and sends it, local-only verbs named as the stated exceptions; cost: a per-verb classification pass.
- Restate the invariant as the classes that exist (control-API verbs, local-only verbs, per-protocol-credential verbs); cost: the same classification, without the fix.

The ruling decides how the concept describes credentials per verb; compose is fixed regardless.

## Ruling

> Recommended ruling (/verify-issues): Fix the compose verbs to send the key they parse (the same resolution every other verb uses), then make the universal true for every verb that dials the control API and name the local-only verbs as the exceptions in the concept.
>
> Rationale: the invariant's value is that an operator never wonders whether a verb honours the key; a class list makes them look it up, and the compose drop is a plain defect the compose-namespace issue also turns on. Flip case: if the conformance verbs are meant to authenticate to peers by a separate credential permanently, name that as a third stated exception rather than forcing the general flag onto them.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
