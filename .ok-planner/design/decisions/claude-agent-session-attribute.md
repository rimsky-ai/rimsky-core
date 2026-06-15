---
decision: claude-agent-session-attribute
status: as-is
aliases: []
---

# claude-agent CLI session token rides the carry-forward attribute surface

## Choice

claude-agent's expected-attributes schema gains a session-token property: a string-typed, executor-written carry-forward attribute, empty by default, describing the agent CLI session token that carries forward in scope across dispatches and which the agent uses to resume the prior CLI conversation when non-empty.

On dispatch: the executor reads the session-token attribute from incoming attributes; if non-empty, launches the CLI with its session-resume flag set to that token. The CLI's session id for this dispatch is the per-dispatch run id (passed to the spawn as the CLI's session id; the same value is also used as the session token on the park path today). On terminal, the agent writes that current dispatch's run id to the session-token attribute via the attributes-writeback callback. The next dispatch in the same RunScope receives that value through carry-forward and resumes the prior CLI conversation. The existing park-path session-token plumbing on the resume-context stays in place for the park use case; the new attribute-driven path is independent and is what build/validate-style loops use.

## Rationale

Aligns claude-agent's session-resume with the carry-forward semantics; removes dependence on the park resume context for the loop case; new RunScope (sub-graph invocation) = no carry-forward source = empty session-token attribute = fresh CLI conversation — exactly the "fresh context per pass" behavior the orchestrator wants.
