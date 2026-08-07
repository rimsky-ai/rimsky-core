---
issue: retry-backoff-numerics-unvalidated
kind: human
category: enforcement-gap
artifacts:
  - concept:error-policy
  - decision:in-place-retry
status: verified
opened: 2026-08-07T08:49:24Z
github: https://github.com/rimsky-ai/rimsky-core/issues/77
---

# A backoff block that produces no backoff registers cleanly

A template author can attach a `retry_backoff` block to a node to control how
long rimsky waits between retries of a failed step: a kind (linear or
exponential), a jitter mode, a base delay, and a ceiling. The two named
vocabularies are closed at registration — an unknown kind or jitter is rejected
with a message listing the legal values. The two numbers beside them are not
checked at all (`lib/graph/node/template_validator.go::validateRetryBackoff`
switches on the kind and the jitter and inspects nothing else).

Three shapes get through, and each one silently produces behavior the author
did not ask for:

- **A negative base delay** is accepted, then clamped to zero when the delay is
  computed (`lib/graph/node/backoff.go::ComputeDelay`). Retries fire back to
  back with no wait.
- **An omitted base delay** is the same thing by a different route: the field is
  a plain int with `omitempty`, so writing a `retry_backoff` block that sets
  only the kind and the jitter yields a base of zero — and every backoff curve
  multiplies that base, so the whole block computes to zero on every attempt.
  The author configured backoff and got a hot retry loop.
- **A ceiling below the base** is accepted and clamps every attempt to the
  ceiling, so the curve the author chose never takes effect and each retry waits
  the same amount.

None of these is diagnosable from the outside. The template registers clean, the
node runs, and the only symptom is retry timing that does not match what the
template says — which looks like a rimsky bug to whoever hits it.

There is a settled idiom for the negative case already. The three dispatch
deadline fields on the same node go through a validator that rejects any
negative duration while keeping zero as the legal "not set" sentinel
(`lib/graph/node/template_validator.go::validateDispatchDeadlines`). The
project's conventions call for one idiom per job repo-wide, so a second numeric
knob on the same node accepting negatives is that idiom applied inconsistently
rather than an open question.

The zero case is genuinely different, because a plain int cannot distinguish
"the author wrote 0" from "the author wrote nothing" — but the enclosing block
can: `retry_backoff` is itself optional, so its presence is the author's
statement that they want backoff. The ceiling case is different again: it is not
about a value being wrong on its own, but about two values being ordered
incoherently.

## Options

- **Reject all three at registration** — negative values, a present block with
  no positive base delay, and a ceiling below the base. Closes the surface
  completely; costs a schema tightening that would reject any existing template
  relying on one of the three accidents.
- **Reject only negatives**, matching the dispatch-deadline idiom exactly, and
  leave the zero and ordering cases alone. Smallest change; leaves the hot-retry
  loop — the most damaging of the three — reachable.
- **Change the fields to pointers** so an omitted value is distinguishable from
  an explicit zero, then warn on the ambiguous cases. Most precise; costs a
  spec-shape change and introduces a warning where the other numeric checks on
  the same node produce errors.
- **Leave the numerics open deliberately** and say so, on the grounds that a
  continuous range has no obviously wrong value. Free, and it means the
  registration check is thorough about the two fields where mistakes are cheap
  and silent about the two where they are not.

The ruling decides how far registration goes in rejecting a backoff
configuration that cannot do what it says.

## Ruling

> Recommended ruling (/verify-issues): reject all three at registration. A
> negative delay is already rejected for every other duration on the same node,
> so accepting one here is just that rule applied unevenly. A backoff block with
> no positive base delay computes to zero on every attempt no matter which curve
> or jitter is set — the block is optional, so writing one is the author saying
> they want a wait, and rimsky should refuse the configuration rather than
> quietly hand them a hot retry loop. And a ceiling below the base makes the
> chosen curve unreachable, which is incoherent on its face rather than merely
> aggressive.
>
> Rationale: the whole point of closing the kind and jitter vocabularies at
> registration was that a misconfigured retry is invisible until production
> traffic hits it, and every argument for that applies with more force to the
> numbers — a typo'd kind is a loud error today, while a typo'd base delay is a
> service hammering a failing dependency with no pause. The narrower option
> would fix the case that costs least and leave the one that costs most. The
> pointer-based option buys a distinction that stops mattering once the presence
> of the block is what carries the author's intent. What would change this call:
> a real template that sets a backoff block deliberately expecting immediate
> retries — that would make the zero case a legitimate configuration rather than
> an accident, and only the negative and ordering checks should land.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
