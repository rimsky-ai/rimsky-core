---
issue: handler-context-has-no-scratch-accessor
kind: audit
category: conflicting
artifacts:
  - decision:inproc-handler-interface
status: promoted
opened: 2026-08-16T10:00:01Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The in-process handler decision describes a scratch accessor and an error translation that live elsewhere

Bundled executors can run in-process; a decision describes the handler interface: one execute method taking generated types, a handler-context struct giving side channels ("scratch access, cascade-message sending"), and an in-process client that translates a handler error into an error terminal. The struct has exactly one field — the message sender — and no scratch accessor: scratch rides the request and outcome messages like any payload, per the scratch-protocol decision, and no builtin handler touches it. And the in-process client returns a handler error unchanged; the shared dispatch layer translates it for every transport alike. No code changes; the prose is stale in three places. The ruling corrects it.

## Options

- Rewrite the three clauses: drop scratch from the context's side channels, say scratch travels on the main messages, and put the error translation on the shared dispatch layer; cost: none.
- Add a scratch accessor to the struct; cost: a second, unused scratch channel — not a live option.

The ruling corrects the description.

## Ruling

> Generated ruling (/verify-issues): Rewrite the decision's Choice so the handler context is described as carrying the cascade-message sender only, scratch as riding the request and outcome messages, and error-to-terminal translation as the shared dispatch layer's act for every transport. Forced by the current-state-only rule; the scratch-protocol decision already fixes where scratch lives. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
