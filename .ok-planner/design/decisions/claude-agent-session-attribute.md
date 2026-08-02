---
decision: claude-agent-session-attribute
---

# claude-agent CLI session token rides attributes on Success, scratch on Park

## Choice

claude-agent's expected-attributes schema carries a session-token property: a string-typed, executor-written carry-forward attribute, empty by default, holding the agent CLI session token. The token rides two channels depending on which settling terminal carries it. On Success (the cascade-self-edge/loop case) the executor writes the current dispatch's run id into the attribute delta, which the next dispatch in the same RunScope receives via ordinary attribute carry-forward. On Park (the rate-limit case), which carries no attribute delta, the executor writes the token into the Park scratch slot instead; the parked row is the same row that re-dispatches at resume, so the scratch value is read back verbatim. On dispatch the executor reads scratch first and falls back to the carried-forward attribute; a non-empty resolved token resumes the CLI conversation. A sub-graph invocation arrives with no session-token attribute and no inherited scratch (the child RunScope's carry-forward source is empty), yielding a fresh CLI conversation. Because a RunScope lives in exactly one frame (per `decision:run-scope-is-per-frame`), session-token continuity is intra-frame on both legs; cross-frame session continuity is not provided by either channel and must be expressed by ferrying the token through a message body via the standard message-borne cross-frame coupling path (see `concept:message` and `concept:cascade`).

## Rationale

Aligns claude-agent's session-resume with the carry-forward semantics for the case that can use them (Success), while routing Park's leg through scratch — the channel purpose-built for opaque executor-private state crossing the park boundary — because per `decision:no-resume-context` Park carries no attribute delta and no dedicated resume-context payload, so it has no attribute channel available. One dispatch-time read (scratch first, attribute fallback) covers both legs uniformly without an executor-visible branch. Sub-graph invocation creates a child RunScope (per `concept:run-scope`) whose carry-forward source is empty and which starts with no inherited scratch, so the agent starts fresh in the child — the natural "fresh context per child invocation" behavior orchestrators want, without a separate reset mechanism.

## Alternatives

- Routing both legs through the attribute delta — rejected: Park carries none; that leg has no attribute channel to use.
- Routing both legs through scratch — rejected: Success already carries the token reliably through the standard attribute carry-forward path, and no boundary forces it off that channel.
- A dedicated Park-side resume-context channel — rejected per `decision:no-resume-context`: a second executor-private carry channel duplicating scratch's job.
