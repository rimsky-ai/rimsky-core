---
issue: concept-blob-backend-pre-v1-staged-framing
kind: audit
category: unclear
artifacts:
  - concept:blob-backend
status: verified
opened: 2026-07-25T03:18:31Z
---

# One parenthetical implies a post-v1 behavior change nobody has ever specified

Rimsky's design documents describe the project as it stands — a house rule bans roadmap language ("later", "post-v1", "for now") because a doc that gestures at future behavior invites building against a plan that doesn't exist. The concept for the blob backend — the pluggable component storing data too large to keep inline, with filesystem, database, and in-memory implementations — has an invariant stating that a stored blob is skipped forever by the cleanup sweep, its bytes retained indefinitely, if it was written under a different backend than the one currently configured (say, after an operator switches storage types). Fine so far. But the sentence adds "(e.g. after an operator switches backends pre-v1)" — a qualifier implying something different happens after v1, which no document anywhere states.

The mechanics suggest the behavior is permanent, not staged: a running process can only reach the bytes of its own configured backend, so cross-backend cleanup isn't a shortcut awaiting v1 — it's structurally impossible without a reconciliation tool nobody has proposed. The project's blanket "pre-v1, break freely" policy doesn't cover this either; its stated scope is wire protocol and config shape, not retention behavior.

## Options

- **Drop the qualifier**: state skip-and-retain as permanent architecture, no roadmap implied.
- **Make the roadmap real**: state current behavior plainly and file the future reconciliation capability as its own queued idea — honest only if such a tool is genuinely intended.
- **Treat "pre-v1" as stray illustrative color** and delete just that word — the minimal edit, same net effect as the first option.

The ruling decides one thing: is indefinite cross-backend retention permanent architecture, or a genuine future gap that deserves a queued idea?

## Ruling

> Recommended ruling (/recommend-rulings): Drop the 'pre-v1' qualifier
> from the example parenthetical and state skip-and-retain as
> permanent architecture. No queued reconciliation story.
>
> Rationale: The behavior is architecturally forced — a process cannot
> reach another backend's bytes — so the qualifier was illustrative
> color that reads as a roadmap; current-state-only says state it
> plainly.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
