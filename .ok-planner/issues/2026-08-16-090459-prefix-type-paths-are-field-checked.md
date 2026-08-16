---
issue: prefix-type-paths-are-field-checked
kind: audit
category: conflicting
artifacts:
  - concept:signal
status: verified
opened: 2026-08-16T09:04:59Z
---

# Prefix subscriptions are field-checked at registration, where the signal concept says they bind loosely

A subscription names a signal type path — exact (one terminal class) or prefix (a whole family such as every terminal error, every transient retry, every attribute change) — and may carry a predicate over the payload. The signal concept says a prefix path binds its payload dynamically, so a predicate over it is not checked against a schema at registration. The code strips the trailing wildcard, resolves the family's shared schema, and field-checks the predicate exactly as for an exact path — stricter than the promise. The failure mode is a refusal at registration for a misspelled field, not a silent gap. The ruling decides whether the concept adopts the stricter behaviour or the code loosens.

## Options

- Amend the concept: prefix paths resolve to the family's shared payload schema and predicates over them are field-checked like exact paths; cost: none — the families do share one message each.
- Make prefix paths dynamic as written; cost: a misspelled field in a family predicate would compile and never match.

The ruling decides whether a family predicate is checked.

## Ruling

> Recommended ruling (/verify-issues): Keep the stricter code and amend the concept — a prefix type path resolves to the family's shared schema, and predicates over it are checked at registration like any other.
>
> Rationale: each prefix family shares one payload message, so the check is sound, and a registration-time refusal beats a predicate that silently never fires — the same reason the corpus checks exact paths. Flip case: if a family ever splits into payloads with different fields, dynamic binding for that family becomes the only honest choice and the concept should say which.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
