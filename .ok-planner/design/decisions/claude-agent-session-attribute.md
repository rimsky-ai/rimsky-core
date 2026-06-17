---
decision: claude-agent-session-attribute
status: as-is
aliases: []
---

# claude-agent CLI session token rides the carry-forward attribute surface

## Choice

claude-agent's expected-attributes schema carries a session-token property: a string-typed, executor-written carry-forward attribute, empty by default, describing the agent CLI session token. On dispatch the executor reads `req.attributes.session_token`; if non-empty, launches the CLI with `--resume <token>`. On every settling terminal (Success in the loop case, Park in the rate-limit case), the executor writes the current dispatch's run id to `attributes_delta.session_token`. The next dispatch in the same RunScope receives that value via carry-forward and resumes the prior CLI conversation. A sub-graph invocation arrives with no session-token attribute, yielding a fresh CLI conversation.

## Rationale

Aligns claude-agent's session-resume with the carry-forward semantics; one path covers both the loop case and the park case. New RunScope (sub-graph invocation) = no carry-forward source = empty session-token attribute = fresh CLI conversation — exactly the "fresh context per pass" behavior the orchestrator wants.
