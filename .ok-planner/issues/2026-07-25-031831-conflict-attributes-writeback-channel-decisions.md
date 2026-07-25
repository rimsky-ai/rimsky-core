---
issue: conflict-attributes-writeback-channel-decisions
kind: audit
category: conflicting
artifacts:
  - decision:uniform-attributes-delta
  - decision:writeback-bumps-progress
  - decision:keepalive-endpoint
status: verified
opened: 2026-07-25T03:18:31Z
---

# One decision doc says a live, tested feature was retired

When rimsky hands work to an executor — a service doing HTTP calls, running an LLM agent, whatever — the executor can attach "attributes," arbitrary key/value data, to the node it's working on. There are two conceivable channels for that: bundle the attributes into the final verdict when the work finishes, or send incremental updates mid-run through a callback endpoint. One decision document flatly states the mid-run channel was retired and the final verdict is now the only channel. But two other decision documents, two concept documents, and the running code all treat the mid-run callback as alive and load-bearing: the route is wired into the supervisor's HTTP handling, has a real handler, is exercised by tests, and doubles as a liveness mechanism — each callback resets the run's "last progress" timestamp, which is exactly the reason a sibling decision gives for building a separate keepalive endpoint instead of reusing it.

So the corpus contradicts itself, and the code has already voted: the channel exists. The question is whether the code is right.

## Options

- **Treat the "retired" sentence as the stale artifact** — reword that one decision to scope its claim (the *terminal* attribute channel is uniform; the incremental writeback coexists). No code change; four other artifacts stand as written.
- **Treat the "retired" sentence as the intended design** — actually delete the mid-run callback from the code and rewrite the four artifacts that describe it. A real feature removal, motivated by nothing but the one sentence.

The ruling decides which artifact set is authoritative.

## Ruling

> Recommended ruling (/recommend-rulings): The mid-dispatch attribute
> writeback is the intended design: amend decision:uniform-attributes-
> delta to scope its retirement claim to the terminal-verdict channel,
> describing coexistence with the incremental writeback the other four
> artifacts and the code document.
>
> Rationale: Four artifacts plus live, tested, liveness-bearing code
> against one decision's sentence — the sentence is the stale
> artifact. Deleting a working channel nothing motivates would be a
> feature removal in service of a typo-grade drift.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
