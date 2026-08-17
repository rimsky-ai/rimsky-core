---
issue: malformed-cursor-answers-500-with-store-error-text
kind: audit
category: inconsistent
artifacts:
  - concept:control-api
status: promoted
opened: 2026-08-16T09:35:03Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# A malformed pagination cursor answers 500 with the store's error text

Collection routes take a limit, sometimes an active filter, and a cursor. Limit and active are validated and answer 400 when malformed; a malformed cursor decodes in the store, and the resulting error matches none of the sentinels the error writer maps, so it falls to the 500 default and — on the observability handlers — echoes the store's message verbatim. A client's own bad input reads as a retryable platform fault, and the body discloses an internal operation name. The ruling classifies the cursor like its siblings.

## Options

- Classify a cursor decode failure as 400 with a caller-safe message on both HTTP layers, matching limit and active; cost: none.
- Also record an invariant that every collection route validates its inputs, with an enumerating check; cost: an ongoing discipline — optional beyond the fix.

The ruling fixes the one inconsistency; broader validation discipline is the owner's separately.

## Ruling

> Generated ruling (/verify-issues): Treat a malformed cursor as a client error — 400 with a safe message — on the control API and the observability handlers alike, matching how the same routes already treat limit and active. Forced by the one-idiom-per-job rule: three query parameters on one route, two classified correctly, one escaping to the default. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
