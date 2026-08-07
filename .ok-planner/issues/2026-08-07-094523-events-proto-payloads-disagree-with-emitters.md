---
issue: events-proto-payloads-disagree-with-emitters
kind: human
category: inconsistent
artifacts:
  - concept:event-log
  - decision:event-log-payload-shapes
status: verified
opened: 2026-08-07T09:45:23Z
github: https://github.com/rimsky-ai/rimsky-core/issues/83
---

# Even the event payloads that do get emitted have the wrong fields

rimsky publishes a protobuf schema describing the payload of each kind of event
it writes to its event log. A separate issue covers the messages in that schema
with no emitter at all
(`issue:typed-event-oneof-is-aspirational`); this one is about the ones that do
emit. Their field lists still disagree with what the emitting code writes — in
both directions, on the same messages.

Keys that arrive but are not declared: the lock-acquired event writes an alias
and an intent; the lock-released event writes an alias; the orphan-reaper event
writes a claim-handle id, a holder node id, an expiry, a claimed-at timestamp,
and an intent. A consumer written against the schema does not know to look for
any of them.

Fields that are declared but never arrive: scope data, a claim id, and a resumed
flag on lock-acquired; a claim id on lock-released; four fields on the
orphan-reaped event; an omitted-fields list on attribute substitution; an
operator-override actor and reason, where only the action is ever written. One
of them is worse than absent — the template-resolution-failed event declares a
field naming which template field failed, and all three emit sites hardcode it
to the empty string, so it is present, always blank, and indistinguishable from
a genuine empty value.

There is also a shape mismatch rather than a membership one. The
attributes-validation-failed payload declares a repeated list of structured
violations, but the live emitter for schema failures always writes exactly one
element containing a single message key — the validator's aggregated string. The
per-violation structure the schema advertises is flattened away at the event
boundary, so a consumer that iterates violations gets one blob of prose.

The mechanism behind all of it is the same as its sibling issue: nothing
constructs the typed message, so nothing fails to compile when an emitter adds
a key or a schema field loses its writer. The emitters build plain maps; the
schema is edited by hand; the two have no mechanical relationship.

The corpus commits to "typed oneof payloads for a settled subset" of event
kinds without naming the subset, so it does not decide whether these particular
messages are contracts to be corrected or documentation to be replaced.

## Options

- **Correct every message against its emitter** — add the missing keys, delete
  the unwritten fields, reshape the violations list. Restores accuracy now;
  leaves the same hand-maintained arrangement that produced the drift, so it
  will recur.
- **Retire the typed schema and generate the published shapes from the emit
  sites**, which dissolves this issue rather than fixing it. Costs a proto
  surface removal and typing the emit sites.
- **Correct only the actively misleading cases** — the always-blank field name
  and the collapsed violations list — and accept the rest as approximate. Least
  work; leaves a schema that is knowingly partly wrong.

The ruling decides whether these payload descriptions are corrected in place or
replaced by something derived from the code that emits them.

## Ruling

> Recommended ruling (/verify-issues): resolve this with its parent rather than
> separately — retire the hand-maintained event schema and publish payload
> shapes derived from what the emitters actually construct, per the ruling on
> `issue:typed-event-oneof-is-aspirational`. Every mismatch listed here is a
> symptom of the schema and the emitter being two separate hand-edited things,
> so deriving one from the other closes all of them at once and keeps them
> closed.
>
> Rationale: correcting the field lists in place — the first option — treats a
> dozen instances of one defect as a dozen defects, and the moment it merges the
> next emitter change reopens it, because nothing would still be holding the two
> in agreement. The partial fix is worse on the same axis: it leaves a published
> schema its own authors know to be wrong in places they chose not to list. What
> would change this call: if the owner keeps the typed schema for its own
> reasons, then this issue becomes real work in its own right, and the
> always-blank template field and the collapsed violations list should lead —
> those two actively mislead a consumer, where a merely-absent field only
> under-informs one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
