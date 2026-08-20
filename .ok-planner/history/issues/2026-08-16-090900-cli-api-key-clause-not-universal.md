---
issue: cli-api-key-clause-not-universal
kind: audit
category: conflicting
artifacts:
  - concept:rimsky
status: promoted
opened: 2026-08-16T09:09:00Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Six CLI verbs define no api-key flag, and the compose verbs drop the key they parse

The rimsky concept's api-key invariant says every verb accepts an api-key flag, falls back to the environment variable, and sends the key as the bearer token. The CLI breaks that invariant twice. The compose verbs (up, down, plan, status) parse the key flag and then build their client without attaching it. An operator who supplies a valid key gets a 401 that reads as a server fault. And six of 26 verbs define no key flag at all: context current, agent status, agent start, compose run, and two conformance verbs. Each of the six plausibly dials no control API or authenticates by a different credential. The concept does not tell a reader which. The ruling decides whether the concept makes the universal true or restates it as classes. The compose credential drop is a bug under either.

## Options

- Make the universal true: every verb that dials the control API takes the general key flag and sends it, and the concept names the local-only verbs as the stated exceptions; cost: a per-verb classification pass.
- Restate the invariant as the classes that exist: control-API verbs, local-only verbs, per-protocol-credential verbs; cost: the same classification pass, without the fix.

The ruling decides how the concept describes credentials per verb. Compose is fixed regardless.

## Ruling

> Recommended ruling (/verify-issues): Fix the compose verbs to send the key they parse, using the same resolution every other verb uses. Then make the universal true for every verb that dials the control API, and name the local-only verbs as the exceptions in the concept.
>
> Rationale: the invariant's value is that an operator never wonders whether a verb honours the key. A class list makes the operator look it up. The compose drop is a plain defect that the compose-namespace issue also turns on. Flip case: if the conformance verbs are meant to authenticate to peers by a separate credential permanently, name that as a third stated exception rather than forcing the general flag onto them.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
