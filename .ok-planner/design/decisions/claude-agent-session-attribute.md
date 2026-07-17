---
decision: claude-agent-session-attribute
status: as-is
aliases: []
---

# claude-agent CLI session token rides attributes on Success, scratch on Park

## Choice

claude-agent's expected-attributes schema carries a session-token property: a string-typed, executor-written carry-forward attribute, empty by default, describing the agent CLI session token. On dispatch the executor reads the session token from scratch first; if scratch is empty, it falls back to `req.attributes.session_token`; if the resolved token is non-empty, it launches the CLI with `--resume <token>`. On Success (the cascade-self-edge/loop case) the executor writes the current dispatch's run id to `attributes_delta.session_token`, which the next dispatch in the same RunScope receives via ordinary attribute carry-forward. On Park (the rate-limit case), which cannot carry `attributes_delta`, the executor instead writes the token into scratch on the Park scratch slot; because the parked row is the same row that re-dispatches at resume, that scratch value is read back verbatim. A sub-graph invocation arrives with no session-token attribute and no inherited scratch (the child RunScope's carry-forward source is empty), yielding a fresh CLI conversation.

## Rationale

Aligns claude-agent's session-resume with the carry-forward semantics for the case that can use them (Success), while routing Park's leg through scratch — the channel purpose-built for opaque executor-private state — because Park does not carry `attributes_delta` and so has no attribute channel available. One dispatch-time read (scratch first, attribute fallback) covers both legs uniformly without an executor-visible branch. Sub-graph invocation creates a child RunScope (per `concept:run-scope`) whose carry-forward source is empty and which starts with no inherited scratch, so the agent starts fresh in the child — the natural "fresh context per child invocation" behavior orchestrators want, without a separate reset mechanism.

## Scope: intra-frame only

Because a RunScope lives in exactly one frame (per `decision:run-scope-is-per-frame`), session-token continuity is intra-frame on both legs: attribute carry-forward preserves the CLI conversation across cascade-self-edge dispatches of the agent node within one frame's RunScope, and park-resume preserves it across the parked row's own resume within that same RunScope. It does not preserve the CLI conversation across frames. Cross-frame session continuity (e.g. a template that wants an agent's CLI session to span operator-triggered frames or message-driven iterations) is not provided by either channel and must be expressed by ferrying the session token through a message body via the standard message-borne cross-frame coupling path (see `concept:message` and `concept:cascade`).
