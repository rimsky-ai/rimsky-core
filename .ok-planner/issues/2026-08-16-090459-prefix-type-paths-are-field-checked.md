---
issue: prefix-type-paths-are-field-checked
kind: audit
category: conflicting
artifacts:
  - concept:signal
status: verified
opened: 2026-08-16T09:04:59Z
---

# The code field-checks prefix subscriptions at registration, where the signal concept says they bind loosely

A subscription names a signal type path and may carry a predicate over the payload. An exact path names one terminal class. A prefix path names a whole family, such as every terminal error, every transient retry, or every attribute change. The signal concept says a prefix path binds its payload dynamically, so registration does not check a predicate over it against a schema. The code strips the trailing wildcard, resolves the family's shared schema, and field-checks the predicate as it does for an exact path. The code is stricter than the concept promises. Registration refuses a misspelled field instead of leaving a silent gap. The ruling decides whether the concept adopts the stricter behaviour or the code loosens.

## Options

- Amend the concept: a prefix path resolves to the family's shared payload schema, and registration field-checks predicates over it as it does for an exact path; cost: none, because each family shares one message.
- Make prefix paths dynamic as written; cost: a misspelled field in a family predicate compiles and never matches.

The ruling decides whether registration checks a family predicate.

## Ruling

> Recommended ruling (/verify-issues): Keep the stricter code and amend the concept. A prefix type path resolves to the family's shared schema, and registration checks predicates over it as it checks any other.
>
> Rationale: each prefix family shares one payload message, so the check is sound. A refusal at registration serves the caller better than a predicate that silently never fires, which is why the corpus checks exact paths. Flip case: if a family ever splits into payloads with different fields, dynamic binding becomes the only honest choice for that family, and the concept should say which.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
