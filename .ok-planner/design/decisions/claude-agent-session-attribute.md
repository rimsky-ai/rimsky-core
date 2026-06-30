---
decision: claude-agent-session-attribute
status: as-is
aliases: []
---

# claude-agent CLI session token rides the carry-forward attribute surface

## Choice

claude-agent's expected-attributes schema carries a session-token property: a string-typed, executor-written carry-forward attribute, empty by default, describing the agent CLI session token. On dispatch the executor reads `req.attributes.session_token`; if non-empty, launches the CLI with `--resume <token>`. On every settling terminal (Success in the cascade-self-edge case, Park in the rate-limit case), the executor writes the current dispatch's run id to `attributes_delta.session_token`. The next dispatch in the same RunScope receives that value via carry-forward and resumes the prior CLI conversation. A sub-graph invocation arrives with no session-token attribute (the child RunScope's carry-forward source is empty), yielding a fresh CLI conversation.

## Rationale

Aligns claude-agent's session-resume with the carry-forward semantics; one path covers both the cascade-self-edge case and the park case. Sub-graph invocation creates a child RunScope (per `concept:run-scope`) whose carry-forward source is empty, so the agent starts fresh in the child — the natural "fresh context per child invocation" behavior orchestrators want, without a separate reset mechanism.

## Scope: intra-frame only

Because a RunScope lives in exactly one frame (per `decision:run-scope-is-per-frame`), session-token carry-forward is intra-frame: it preserves the CLI conversation across multiple dispatches of the agent node that all share one frame's RunScope (typically cascade self-edges or park-resume within the same frame). It does not preserve the CLI conversation across frames. Cross-frame session continuity (e.g. a template that wants an agent's CLI session to span operator-triggered frames or message-driven iterations) is not provided by this attribute mechanism and must be expressed by ferrying the session token through a message body via the standard message-borne cross-frame coupling path (see `concept:message` and `concept:cascade`).
