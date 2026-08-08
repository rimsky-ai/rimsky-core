---
issue: error-type-reason-template-never-substituted
kind: human
category: bug
artifacts:
  - concept:error-policy
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T08:49:18Z
github: https://github.com/rimsky-ai/rimsky-core/issues/70
---

# A field called reason_template is not a template

A template author can declare how a node responds to each class of error —
retry it, give up, hand it back to the queue — and attach a `reason_template`
string that gets recorded on the resulting outcome. The name says the string is
a template: something with placeholders that get filled in with the specifics
of the failure.

Nothing fills anything in. The evaluator that resolves an error policy copies
the string to the outcome's reason verbatim (`lib/graph/node/policy.go#63`),
and there is no substitution logic for it anywhere in the tree. Write
`retry {{attempt}} of {{max}}` and every recorded reason reads, literally,
`retry {{attempt}} of {{max}}`.

Nobody is misled at runtime — the value lands where it should and is perfectly
usable as a static label. The cost is entirely up front, on the author who
reads the field name, assumes interpolation because that is what "template"
means, writes placeholders, and only finds out by reading the recorded output.
There is no error and no warning; the braces simply survive.

rimsky does have a substitution grammar, used elsewhere for filling values into
node configuration from upstream results. This field is not part of it, and no
other field in the spec package with a `Template` suffix carries substitution
semantics — so there is no established idiom here that settles which way this
should go.

The corpus is silent: the error-policy concept describes the field's effect
correctly, without committing to whether the string is interpolated.

## Options

- **Rename it to `reason`.** The behavior is then exactly what the name says.
  Costs a rename on a template-visible field — cheap while pre-v1, and any
  existing template using it needs the key changed.
- **Implement substitution.** Gives authors error reasons that name the actual
  attempt count or error class. Costs a decision about which grammar and which
  values are in scope, plus the validation to reject placeholders that name
  something unavailable — real feature work, not a patch.
- **Leave it and document the name as a misnomer.** Free, and it means the
  project ships a field whose name is known to describe something it does not
  do.

The ruling decides whether this string becomes interpolated or gets a name that
matches what it is.

## Ruling

> Recommended ruling (/verify-issues): rename it to `reason`. It is a static
> label, it works correctly as one, and the only defect is a name promising
> behavior that was never built — so make the name true rather than building the
> behavior to match a name nobody chose deliberately.
>
> Rationale: implementing substitution means picking a grammar, deciding which
> failure values are in scope, and validating placeholders that name something
> unavailable — a feature, arriving through the side door of a naming defect,
> and the project already has one substitution grammar whose boundaries are
> deliberately closed. Nothing has asked for interpolated error reasons; what
> has been observed is authors misreading the field. Renaming is also cheapest
> to reverse: if interpolated reasons are wanted later, they can be designed
> then, on their own terms. What would change this call: a concrete case where
> a static reason is genuinely insufficient — an operator who needs the attempt
> count in the reason and cannot get it from the signal payload beside it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
