---
issue: template-delete-refusal-escapes-as-server-error
kind: audit
category: inconsistent
artifacts:
  - concept:template
  - concept:control-api
status: verified
opened: 2026-08-16T09:40:01Z
---

# Deleting a template still referenced by a terminated instance answers 500 with a raw constraint error

Deleting a template is refused while instances reference it. The handler's pre-check counts only active instances, so a template referenced solely by a terminated instance reaches the store delete; the store maps foreign-key violations to a template-in-use error (rendered 409) — but its predicate matches only the plain foreign-key error code, and SQLite enforces restrict-on-delete through an internal trigger that reports a different code, so the mapping never fires and the raw driver string escapes as a 500. Postgres is unaffected. The ruling widens the store's predicate.

## Options

- Have the store's foreign-key predicate match SQLite's trigger-constraint code as well; cost: none — one site, no other caller.
- Also widen the handler's pre-check to count referencing instances of every state, for the friendlier message; cost: a small UX improvement, optional.
- Add a decision requiring a test per mapped constraint code; cost: process work, optional.

The ruling fixes the escape; the extras are the owner's.

## Ruling

> Generated ruling (/verify-issues): Make the SQLite store's foreign-key mapping recognise the trigger-constraint code SQLite reports for restrict-on-delete, so the referenced-template refusal reaches the existing template-in-use error and its 409. Forced by the plain defect — the mapping branch exists and is dead for the code SQLite actually returns. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
