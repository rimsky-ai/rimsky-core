---
issue: three-signal-payload-fields-have-no-emit-site
kind: human
category: bug
artifacts:
  - concept:signal
  - concept:attribute
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T08:55:57Z
github: https://github.com/rimsky-ai/rimsky-core/issues/80
---

# Three signal payload fields are declared, checkable, and never written

When a node retries, parks, or settles, rimsky emits a **signal** (a typed
event with a payload) that downstream subscribers filter on with a predicate
expression. The field names available to that expression come from Go structs,
and rimsky binds those field types at template registration so a predicate
naming a field that does not exist is rejected up front. That check is the
whole reason the structs exist.

Three declared fields are never written by anything: the retry payload's `cap`
and `discarded_claims`, and the attribute-changed payload's `old_value`. Each
appears only in its struct definition and the struct's own unit test. Because
they *are* declared, a predicate on any of them passes registration cleanly —
and then never matches, for the lifetime of the deployment, with no error at
any point. That is the worst possible failure shape for a filter: the author
gets a green light from the one mechanism built to catch exactly this mistake.

The data is not missing in two of the three cases. The retry budget is already
in hand where the retry signal is built (`lib/runtime/runner_error_policy.go::errorPolicySignal`
constructs the payload as a literal map of `attempt` / `error_class` /
`delay_ms` / `error_payload`, with the cap sitting right there). The prior
attribute value is likewise already loaded and then discarded — the diff that
produces the change event computes the old value and drops it on the floor
(`lib/runtime/attribute_cascade.go::emitAttributeChangesForRunInTx` writes
`key` and `value` only). For both, "wire it" means passing a value that the
call site already holds.

`discarded_claims` is a different animal. It is declared on the **retry**
payload, but a plain retry deliberately keeps every claim held; the action
that actually discards claims is `release_and_requeue`, which emits its own
signal type carrying only `error_class` and `error_payload`. So the field is
not merely unwired — it is on the wrong payload, and there is no correct
value it could ever carry where it currently sits. Moving it means deciding
what the release-and-requeue payload should look like, which is a schema
question rather than a plumbing one.

The corpus does not decide this. `concept:signal` states that a type-path
resolves to one payload shape, but says per-type field membership "is owned by
the emission code and the CEL environment construction, not enumerated here" —
which makes the emission code the authority and leaves the two live directions,
emit or delete, equally legal.

## Options

- **Wire all three.** Honest schema, no surface loss — but `discarded_claims`
  has no meaning on a retry, so wiring it requires inventing one.
- **Wire `cap` and `old_value`, drop `discarded_claims`.** Fixes the two fields
  whose values already exist and removes the one that is on the wrong signal;
  costs the surface a name that a future release-and-requeue payload might want
  back.
- **Drop all three.** Cheapest and leaves the schema honest, but throws away
  two genuinely useful fields — a retry budget and a before-value — that
  consumers have to reconstruct from event history instead.
- **Leave them and document them as never-populated.** Preserves the surface at
  the cost of keeping the registration check lying to authors, which is the
  defect itself.

The ruling decides which of these fields rimsky commits to emitting and which
it removes from the published schema.

## Ruling

> Recommended ruling (/verify-issues): emit the retry budget and the previous
> attribute value — both are already computed where the signal is built, and a
> declared field that never arrives is worse than no field at all. Remove the
> discarded-claims field outright: it sits on the retry signal, and a retry by
> definition discards nothing, so no value belongs there. If release-and-requeue
> should report what it let go of, that is its own payload's design and its own
> piece of work.
>
> Rationale: the corpus makes the emission code the authority on which fields a
> payload carries, so any field the emitter does not write is a schema defect by
> construction — the only question is which way to close it, and for two of the
> three the value is already sitting at the call site, which makes deleting them
> a pure loss. The third differs in kind rather than degree: it is misplaced, not
> merely unwired, so the last two options in the list above both amount to
> guessing at a payload shape nobody has designed yet. What would change this
> call: if release-and-requeue is about to gain a schema-bound payload anyway,
> then relocating the field is cheaper than deleting and re-adding it, and it
> should ride that work instead.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
