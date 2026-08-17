---
issue: cli-reports-a-write-that-dry-run-mode-refused
kind: audit
category: conflicting
artifacts:
  - decision:auth-dry-run-mode-floor-on-key
  - decision:auth-dry-run-request-flag
  - concept:rimsky
status: promoted
opened: 2026-08-16T09:40:03Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The CLI reports a write as done when the server previewed it

Under a dry-run-pinned key (a credential the server never lets write), every write returns a preview envelope — dry-run true, the intent's details — with status 200. The CLI's write verbs decode the response body directly into their resource type; the envelope shares no field with any resource type, so the decode succeeds with every field zero and no error, and the verb prints "deployed" (or its equivalent) and exits 0. Thirteen client methods share the pattern; nothing in the CLI knows the envelope exists. A script gating on exit code proceeds as if the write landed. The ruling teaches the client to recognise the envelope.

## Options

- Recognise the top-level dry-run marker at the client's shared decode chokepoint and surface a preview result every write verb prints as "would have"; cost: one place, uniform.
- Per-route preview types recognised before decode; cost: the same effect with more boilerplate.
- Fail closed on an all-zero decoded resource; cost: false positives on legitimately empty responses, and no preview details.

The ruling stops the CLI reporting a write it did not make.

## Ruling

> Generated ruling (/verify-issues): Teach the CLI's shared response decode to recognise the dry-run preview envelope before unmarshalling into a resource, and have every write verb report a preview as a preview — "would have <verbed>", exit 0, structured output marked dry-run — never as a completed write. Forced by the dry-run concept (every write is previewable, and the envelope is the preview) and the mode-floor decision (the floor is identity-bound, so any script can hit it); one chokepoint fixes all thirteen verbs. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
